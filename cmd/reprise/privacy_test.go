package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/nrynss/keel/erase"
	"github.com/nrynss/keel/gate"
	"github.com/nrynss/keel/id"
	"github.com/nrynss/keel/job"
	jobsqlitestore "github.com/nrynss/keel/job/sqlitestore"
	"github.com/nrynss/keel/mediastore"
	"github.com/nrynss/keel/stream"
	"github.com/nrynss/reprise/internal/api"
	"github.com/nrynss/reprise/internal/assemblyai"
	"github.com/nrynss/reprise/internal/identity"
	"github.com/nrynss/reprise/internal/privacy"
	"github.com/nrynss/reprise/internal/retention"
	"github.com/nrynss/reprise/internal/settings"
)

// eraseSessionDouble ends provider voice sessions at once. An ended id
// ends again cleanly, because ending repeats safely.
type eraseSessionDouble struct {
	mu      sync.Mutex
	deleted map[string]bool
}

// TerminateSession records the call and ends the session.
func (d *eraseSessionDouble) TerminateSession(_ context.Context, sessionID string) (assemblyai.TerminateResult, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.deleted == nil {
		d.deleted = map[string]bool{}
	}
	d.deleted[sessionID] = true
	return assemblyai.TerminateResult{Deleted: true}, nil
}

// ended reports whether the double ended sessionID.
func (d *eraseSessionDouble) ended(sessionID string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.deleted[sessionID]
}

// eraseTranscriptDouble removes provider batch transcripts. A deleted
// id reads back as the provider deletion marker, the way the real
// provider keeps soft deleted copies readable.
type eraseTranscriptDouble struct {
	mu      sync.Mutex
	deleted map[string]bool
}

// Delete marks one transcript deleted.
func (d *eraseTranscriptDouble) Delete(_ context.Context, id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.deleted == nil {
		d.deleted = map[string]bool{}
	}
	d.deleted[id] = true
	return nil
}

// Get fetches one transcript with its deletion mark.
func (d *eraseTranscriptDouble) Get(_ context.Context, id string) (assemblyai.Transcript, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.deleted[id] {
		return assemblyai.Transcript{ID: id, Status: "completed", Text: assemblyai.DeletedText}, nil
	}
	return assemblyai.Transcript{ID: id, Status: "completed", Text: "the host asks and the guest answers"}, nil
}

// deletedID reports whether the double deleted id.
func (d *eraseTranscriptDouble) deletedID(id string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.deleted[id]
}

// privacyWorld carries the mounted privacy routes over fixture stores.
type privacyWorld struct {
	mux        *http.ServeMux
	fx         *wireFixture
	guests     *identity.Service
	episodes   *privacy.Service
	sweeps     *retention.Service
	sessions   *eraseSessionDouble
	transcript *eraseTranscriptDouble
	jobStore   *jobsqlitestore.Store
	coverDir   string
}

// openPrivacyWorld builds the diary, the identity service, the media
// store, the scripted provider doubles, and both privacy services,
// then mounts the privacy hook the way the boot does. The holder is
// reset after the test, so no case inherits another case's services.
func openPrivacyWorld(t *testing.T) *privacyWorld {
	t.Helper()
	fx := openWireFixture(t)
	guests, err := identity.New(t.Context(), identity.Config{DB: fx.db, SigningKey: "privacy-test-signing-key"})
	if err != nil {
		t.Fatalf("open identity: %v", err)
	}
	coverDir := filepath.Join(t.TempDir(), "covers")
	if err := os.MkdirAll(coverDir, 0o755); err != nil {
		t.Fatalf("make cover dir: %v", err)
	}
	world := &privacyWorld{
		mux:        http.NewServeMux(),
		fx:         fx,
		guests:     guests,
		sessions:   &eraseSessionDouble{},
		transcript: &eraseTranscriptDouble{},
		coverDir:   coverDir,
	}
	episodes, err := privacy.New(privacy.Config{
		DB:          fx.db,
		Media:       fx.media,
		CoverDir:    coverDir,
		Sessions:    world.sessions,
		Transcripts: world.transcript,
		Owns:        privacy.IdentityOwns(guests),
	})
	if err != nil {
		t.Fatalf("open privacy service: %v", err)
	}
	world.episodes = episodes
	sweeps, err := retention.New(retention.Config{
		DB:          fx.db,
		Media:       fx.media,
		CoverDir:    coverDir,
		Sessions:    world.sessions,
		Transcripts: world.transcript,
		Window:      time.Hour,
	})
	if err != nil {
		t.Fatalf("open retention service: %v", err)
	}
	world.sweeps = sweeps
	store, err := jobsqlitestore.Open(t.Context(), jobsqlitestore.Config{DB: fx.db})
	if err != nil {
		t.Fatalf("open job store: %v", err)
	}
	world.jobStore = store
	runner, err := job.Open(t.Context(), job.Config{
		Broker: stream.New(stream.Config{}),
		Store:  store,
		Kinds: map[string]job.Kind{
			erase.KindName:      episodes.Eraser().Kind(),
			retention.SweepName: sweeps.Kind(),
		},
	})
	if err != nil {
		t.Fatalf("open job runner: %v", err)
	}
	holdPrivacyServices(&privacyServices{episodes: episodes, sweeps: sweeps})
	t.Cleanup(func() { holdPrivacyServices(nil) })
	spendGate, err := gate.New(gate.Config{})
	if err != nil {
		t.Fatalf("open spend gate: %v", err)
	}
	rule := gate.Rule{
		Name:      "privacy-test",
		PerClient: gate.Limit{Burst: 64, Every: time.Second},
		Global:    gate.Limit{Burst: 64, Every: time.Second},
	}
	if err := api.Mount(world.mux, api.Dependencies{
		Gate:     spendGate,
		Rule:     rule,
		Identity: guests,
	}); err != nil {
		t.Fatalf("mount core routes: %v", err)
	}
	if err := mountPrivacy(t.Context(), routeWiring{
		Mux:      world.mux,
		Gate:     spendGate,
		Outer:    rule,
		Guests:   guests.Middleware,
		DB:       fx.db,
		Runner:   runner,
		Media:    fx.media,
		Settings: settings.Settings{MediaDir: fx.pipe.mediaDir},
	}); err != nil {
		t.Fatalf("mount privacy: %v", err)
	}
	return world
}

// privacySeed names the rows one mounted episode owns.
type privacySeed struct {
	episode   string
	opus      string
	aac       string
	session   string
	batch     string
	coverFile string
}

// seedPrivacyEpisode writes one ready episode with a render, stems, a
// provider session, a batch transcript, and a cover file. Blobs land
// through the real media store, so the erase pin measures real files.
func seedPrivacyEpisode(t *testing.T, world *privacyWorld, owner string) privacySeed {
	t.Helper()
	fx := world.fx
	ctx := t.Context()
	episodeID, err := id.New()
	if err != nil {
		t.Fatalf("mint episode id: %v", err)
	}
	token, err := id.New()
	if err != nil {
		t.Fatalf("mint share token: %v", err)
	}
	seed := privacySeed{episode: episodeID}
	if _, err := fx.db.Writer().ExecContext(ctx, `INSERT INTO episodes
		(id, owner_id, number, title, state, visibility, share_token, seeded)
		VALUES (?, ?, 1, 'A quiet take', 'ready', 'private', ?, 0)`,
		episodeID, owner, token); err != nil {
		t.Fatalf("seed episode: %v", err)
	}
	persist := func(contentType string, body []byte) string {
		blob, err := fx.media.Persist(ctx, bytes.NewReader(body), mediastore.Put{
			ContentType: contentType,
			Owner:       owner,
			Group:       episodeID,
			Visibility:  mediastore.Private,
		})
		if err != nil {
			t.Fatalf("persist blob: %v", err)
		}
		return blob
	}
	seed.opus = persist("audio/ogg", []byte("render opus "+episodeID))
	seed.aac = persist("audio/mp4", []byte("render aac "+episodeID))
	renderID, err := id.New()
	if err != nil {
		t.Fatalf("mint render id: %v", err)
	}
	if _, err := fx.db.Writer().ExecContext(ctx, `INSERT INTO renders
		(id, owner_id, episode_id, input_hash, opus_media_id, aac_media_id, loudness)
		VALUES (?, ?, ?, 'hash', ?, ?, -16)`, renderID, owner, episodeID, seed.opus, seed.aac); err != nil {
		t.Fatalf("seed render: %v", err)
	}
	for _, role := range []string{"user", "host"} {
		blob := persist("audio/ogg", []byte("stem "+role+" "+episodeID))
		stemID, err := id.New()
		if err != nil {
			t.Fatalf("mint stem id: %v", err)
		}
		if _, err := fx.db.Writer().ExecContext(ctx, `INSERT INTO stems
			(id, owner_id, episode_id, media_id, role, sample_rate, start_offset_ms)
			VALUES (?, ?, ?, ?, ?, 48000, 0)`, stemID, owner, episodeID, blob, role); err != nil {
			t.Fatalf("seed stem: %v", err)
		}
	}
	seed.session = "prov-session-" + episodeID
	if _, err := fx.db.Writer().ExecContext(ctx, `INSERT INTO sessions
		(id, owner_id, episode_id, provider_session_id, token_cap, connected_seconds)
		VALUES (?, ?, ?, ?, 1800, 60)`, "sess-"+episodeID, owner, episodeID, seed.session); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	seed.batch = "batch-" + episodeID
	analysisID, err := id.New()
	if err != nil {
		t.Fatalf("mint analysis id: %v", err)
	}
	if _, err := fx.db.Writer().ExecContext(ctx, `INSERT INTO analyses
		(id, owner_id, episode_id, transcript_id, chapters, summary, entities, key_phrases)
		VALUES (?, ?, ?, ?, '', '', '', '')`, analysisID, owner, episodeID, seed.batch); err != nil {
		t.Fatalf("seed analysis: %v", err)
	}
	seed.coverFile = episodeID + ".png"
	if err := os.WriteFile(filepath.Join(world.coverDir, seed.coverFile), []byte("cover "+episodeID), 0o600); err != nil {
		t.Fatalf("seed cover: %v", err)
	}
	return seed
}

// servePrivacy runs one mounted request with an optional cookie and
// returns the recorder.
func servePrivacy(world *privacyWorld, method, path string, cookie *http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	world.mux.ServeHTTP(rec, req)
	return rec
}

// publishToken publishes one episode through the mounted routes and
// returns the share token.
func publishToken(t *testing.T, world *privacyWorld, cookie *http.Cookie, episodeID string) string {
	t.Helper()
	rec := servePrivacy(world, http.MethodPost, "/api/episodes/"+episodeID+"/publish", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("publish status %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		ShareToken string `json:"share_token"`
		SharePath  string `json:"share_path"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode publish: %v", err)
	}
	if body.ShareToken == "" || body.SharePath == "" {
		t.Fatalf("publish body names no token or path: %s", rec.Body.String())
	}
	return body.ShareToken
}

// waitPrivacyJob polls one erasure or sweep job until its latest
// attempt lands. A failed latest attempt fails the test with the
// recorded error, so a stuck job fails loudly instead of hanging the
// suite.
func waitPrivacyJob(t *testing.T, world *privacyWorld, jobID string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		attempts, err := world.jobStore.Attempts(t.Context(), jobID)
		if err != nil {
			t.Fatalf("list attempts: %v", err)
		}
		if len(attempts) > 0 {
			switch last := attempts[len(attempts)-1]; last.Status {
			case job.StatusDone:
				return
			case job.StatusError, job.StatusCancelled, job.StatusInterrupted:
				t.Fatalf("job failed: %v", last.Err)
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %s never finished", jobID)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// privacyRowCount counts the rows in table for owner.
func privacyRowCount(t *testing.T, world *privacyWorld, table, owner string) int {
	t.Helper()
	column := "owner_id"
	if table == "users" {
		column = "id"
	}
	var total int
	if err := world.fx.db.Reader().QueryRowContext(t.Context(),
		"SELECT COUNT(*) FROM "+table+" WHERE "+column+" = ?", owner).Scan(&total); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return total
}

// TestPrivacyKindsRegisterEraseAndSweep pins the kinds hook. Both job
// kinds reach the runner under their own names, so a restart resumes
// an erasure and a sweep instead of losing them.
func TestPrivacyKindsRegisterEraseAndSweep(t *testing.T) {
	fx := openWireFixture(t)
	loaded := bootSettings(t, t.TempDir())
	kinds, err := privacyKinds(kindWiring{DB: fx.db, Media: fx.media, Settings: loaded})
	if err != nil {
		t.Fatalf("privacy kinds: %v", err)
	}
	t.Cleanup(func() { holdPrivacyServices(nil) })
	if _, ok := kinds[erase.KindName]; !ok {
		t.Fatalf("kinds miss the erasure kind %q", erase.KindName)
	}
	if _, ok := kinds[retention.SweepName]; !ok {
		t.Fatalf("kinds miss the sweep kind %q", retention.SweepName)
	}
	if _, err := mergeKinds(map[string]job.Kind{bootProbeKind: {Limit: 1}}, kinds); err != nil {
		t.Fatalf("merge privacy kinds: %v", err)
	}
	if _, err := mergeKinds(map[string]job.Kind{erase.KindName: {Limit: 1}}, kinds); err == nil {
		t.Fatal("a repeated erasure kind merged without a refusal")
	}
}

// TestPrivacyShareOpensAndRevokes pins the share round trip. A caller
// with no cookie reads the published episode through its token, with
// its cover, and reads 404 after revoke. A stranger reads 404 on the
// owner routes.
func TestPrivacyShareOpensAndRevokes(t *testing.T) {
	world := openPrivacyWorld(t)
	cookie, owner := finishGuest(t, world.guests)
	seed := seedPrivacyEpisode(t, world, owner)
	token := publishToken(t, world, cookie, seed.episode)

	share := servePrivacy(world, http.MethodGet, "/api/share/"+token, nil)
	if share.Code != http.StatusOK {
		t.Fatalf("share status %d, want 200: %s", share.Code, share.Body.String())
	}
	var payload struct {
		EpisodeID string `json:"episode_id"`
		Title     string `json:"title"`
	}
	if err := json.Unmarshal(share.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode share: %v", err)
	}
	if payload.EpisodeID != seed.episode {
		t.Fatalf("share episode %s, want %s", payload.EpisodeID, seed.episode)
	}
	cover := servePrivacy(world, http.MethodGet, "/api/share/"+token+"/cover", nil)
	if cover.Code != http.StatusOK {
		t.Fatalf("share cover status %d, want 200", cover.Code)
	}
	if contentType := cover.Header().Get("Content-Type"); contentType != "image/png" {
		t.Fatalf("share cover type %q, want image/png", contentType)
	}

	stranger, _ := finishGuest(t, world.guests)
	if rec := servePrivacy(world, http.MethodPost, "/api/episodes/"+seed.episode+"/publish", stranger); rec.Code != http.StatusNotFound {
		t.Fatalf("stranger publish status %d, want 404", rec.Code)
	}

	revoke := servePrivacy(world, http.MethodDelete, "/api/episodes/"+seed.episode+"/publish", cookie)
	if revoke.Code != http.StatusOK {
		t.Fatalf("revoke status %d, want 200: %s", revoke.Code, revoke.Body.String())
	}
	if gone := servePrivacy(world, http.MethodGet, "/api/share/"+token, nil); gone.Code != http.StatusNotFound {
		t.Fatalf("revoked share status %d, want 404", gone.Code)
	}
	if gone := servePrivacy(world, http.MethodGet, "/api/share/"+token+"/cover", nil); gone.Code != http.StatusNotFound {
		t.Fatalf("revoked cover status %d, want 404", gone.Code)
	}
}

// TestPrivacyEraseRemovesEveryCopy pins the erase round trip. The
// erased episode leaves no row, no media file, no cover file, and no
// provider transcript or session.
func TestPrivacyEraseRemovesEveryCopy(t *testing.T) {
	world := openPrivacyWorld(t)
	cookie, owner := finishGuest(t, world.guests)
	seed := seedPrivacyEpisode(t, world, owner)

	rec := servePrivacy(world, http.MethodDelete, "/api/episodes/"+seed.episode, cookie)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("erase status %d, want 202: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode erase: %v", err)
	}
	if body.JobID == "" {
		t.Fatalf("erase body names no job: %s", rec.Body.String())
	}
	waitPrivacyJob(t, world, body.JobID)

	if got := privacyRowCount(t, world, "episodes", owner); got != 0 {
		t.Fatalf("episodes holds %d rows, want none", got)
	}
	var mediaRows int
	if err := world.fx.db.Reader().QueryRowContext(t.Context(),
		"SELECT COUNT(*) FROM media WHERE media_group = ?", seed.episode).Scan(&mediaRows); err != nil {
		t.Fatalf("count media: %v", err)
	}
	if mediaRows != 0 {
		t.Fatalf("media holds %d rows for the erased episode, want none", mediaRows)
	}
	entries, err := os.ReadDir(world.fx.pipe.mediaDir)
	if err != nil {
		t.Fatalf("list media dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("media dir holds %d files, want none", len(entries))
	}
	if _, err := os.Stat(filepath.Join(world.coverDir, seed.coverFile)); !os.IsNotExist(err) {
		t.Fatalf("cover file survives the erase")
	}
	if !world.transcript.deletedID(seed.batch) {
		t.Fatalf("provider transcript %s never deleted", seed.batch)
	}
	if !world.sessions.ended(seed.session) {
		t.Fatalf("provider session %s never ended", seed.session)
	}
	stranger, _ := finishGuest(t, world.guests)
	if rec := servePrivacy(world, http.MethodDelete, "/api/episodes/"+seed.episode, stranger); rec.Code != http.StatusNotFound {
		t.Fatalf("stranger erase status %d, want 404", rec.Code)
	}
}

// TestPrivacySweepExpiresTheIdleGuest pins the scheduled sweep. A
// guest idle past the window loses its episodes, its blobs, its
// provider copies, and its user row in one fixture run.
func TestPrivacySweepExpiresTheIdleGuest(t *testing.T) {
	world := openPrivacyWorld(t)
	cookie, owner := finishGuest(t, world.guests)
	seed := seedPrivacyEpisode(t, world, owner)
	token := publishToken(t, world, cookie, seed.episode)
	if _, err := world.fx.db.Writer().ExecContext(t.Context(),
		"UPDATE users SET last_seen_at = ? WHERE id = ?", time.Now().Add(-2*time.Hour).Unix(), owner); err != nil {
		t.Fatalf("age the guest: %v", err)
	}

	jobID, err := world.sweeps.Sweep(t.Context())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	waitPrivacyJob(t, world, jobID)

	if got := privacyRowCount(t, world, "episodes", owner); got != 0 {
		t.Fatalf("episodes holds %d guest rows, want none", got)
	}
	if got := privacyRowCount(t, world, "users", owner); got != 0 {
		t.Fatalf("users holds %d guest rows, want none", got)
	}
	if got := servePrivacy(world, http.MethodGet, "/api/share/"+token, nil); got.Code != http.StatusNotFound {
		t.Fatalf("swept share reads %d, want 404", got.Code)
	}
	if !world.transcript.deletedID(seed.batch) {
		t.Fatalf("provider transcript %s never deleted", seed.batch)
	}
	if !world.sessions.ended(seed.session) {
		t.Fatalf("provider session %s never ended", seed.session)
	}
}
