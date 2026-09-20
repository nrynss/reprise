package privacy_test

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/nrynss/keel/erase"
	"github.com/nrynss/keel/id"
	"github.com/nrynss/keel/job"
	jobsqlitestore "github.com/nrynss/keel/job/sqlitestore"
	"github.com/nrynss/keel/mediastore"
	mediasqlitestore "github.com/nrynss/keel/mediastore/sqlitestore"
	"github.com/nrynss/keel/sqlite"
	"github.com/nrynss/keel/stream"
	"github.com/nrynss/reprise/internal/assemblyai"
	"github.com/nrynss/reprise/internal/privacy"
	"github.com/nrynss/reprise/internal/store"
)

// ownsFunc checks ownership behind a test variable. The test sets the
// current user, so owner endpoints run without a session cookie.
type ownsFunc func(ctx context.Context, ownerID string) bool

// Owns reports whether ownerID is the current test user.
func (f ownsFunc) Owns(ctx context.Context, ownerID string) bool { return f(ctx, ownerID) }

// sessionDouble records provider session deletes. A set block channel
// holds the delete until it closes, which stages a mid-erase restart.
// An ended id deletes again cleanly, because ending repeats safely.
type sessionDouble struct {
	mu      sync.Mutex
	calls   []string
	block   chan struct{}
	deleted map[string]bool
}

// TerminateSession records the call and ends the session.
func (d *sessionDouble) TerminateSession(ctx context.Context, sessionID string) (assemblyai.TerminateResult, error) {
	if d.block != nil {
		select {
		case <-d.block:
		case <-ctx.Done():
			return assemblyai.TerminateResult{}, ctx.Err()
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls = append(d.calls, sessionID)
	if d.deleted == nil {
		d.deleted = map[string]bool{}
	}
	d.deleted[sessionID] = true
	return assemblyai.TerminateResult{Deleted: true}, nil
}

// ended reports whether the double ended sessionID.
func (d *sessionDouble) ended(sessionID string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.deleted[sessionID]
}

// transcriptDouble records provider batch deletes. A deleted id reads
// back as the provider deletion marker with no words, the way the real
// provider keeps soft deleted copies readable.
type transcriptDouble struct {
	mu      sync.Mutex
	deletes []string
	deleted map[string]bool
	fail    bool
}

// Delete marks one transcript deleted, unless the double refuses.
func (d *transcriptDouble) Delete(ctx context.Context, id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.deletes = append(d.deletes, id)
	if d.fail {
		return errProviderDown
	}
	if d.deleted == nil {
		d.deleted = map[string]bool{}
	}
	d.deleted[id] = true
	return nil
}

// Get fetches one transcript with its deletion mark.
func (d *transcriptDouble) Get(ctx context.Context, id string) (assemblyai.Transcript, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.deleted[id] {
		return assemblyai.Transcript{ID: id, Status: "completed", Text: assemblyai.DeletedText}, nil
	}
	return assemblyai.Transcript{ID: id, Status: "completed", Text: "the host asks and the guest answers"}, nil
}

// errProviderDown is the test refusal the scripted doubles answer with.
var errProviderDown = errTest("provider is down")

// errTest is a test only error.
type errTest string

// Error answers the test refusal.
func (e errTest) Error() string { return string(e) }

// episodeSeed names the rows one fixture episode owns.
type episodeSeed struct {
	episode   string
	owner     string
	session   string
	stems     []string
	opus      string
	aac       string
	stereo    string
	batch     string
	coverFile string
}

// fixture carries an open service with one episode per owner.
type fixture struct {
	t          *testing.T
	db         *sqlite.DB
	svc        *privacy.Service
	media      *mediastore.Store
	mediaDir   string
	coverDir   string
	sessions   *sessionDouble
	transcript *transcriptDouble
	jobStore   *jobsqlitestore.Store
	runner     *job.Runner
	current    string
	seeds      map[string]*episodeSeed
}

// openFixture builds the diary, the media store, the cover directory,
// the scripted provider doubles, and one full episode per owner. Every
// blob lands through the real media store, so pins measure real files
// with real visibility.
func openFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "diary.db")
	db, err := sqlite.Open(t.Context(), sqlite.Config{
		Path:   path,
		Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := store.Open(t.Context(), db); err != nil {
		t.Fatalf("open diary store: %v", err)
	}
	mediaDir := filepath.Join(dir, "media")
	index, err := mediasqlitestore.Open(t.Context(), mediasqlitestore.Config{DB: db})
	if err != nil {
		t.Fatalf("open media index: %v", err)
	}
	media, err := mediastore.Open(t.Context(), mediastore.Config{
		Dir:   mediaDir,
		Index: index,
		ContentTypes: []string{
			"image/png",
			"image/jpeg",
			"image/webp",
			"audio/mpeg",
			"audio/wav",
			"audio/ogg",
			"audio/webm",
			"audio/mp4",
			"video/mp4",
			"application/pdf",
			"text/vtt",
			"application/x-subrip",
		},
	})
	if err != nil {
		t.Fatalf("open media store: %v", err)
	}
	coverDir := filepath.Join(dir, "covers")
	if err := os.MkdirAll(coverDir, 0o755); err != nil {
		t.Fatalf("make cover dir: %v", err)
	}
	fx := &fixture{
		t:          t,
		db:         db,
		media:      media,
		mediaDir:   mediaDir,
		coverDir:   coverDir,
		sessions:   &sessionDouble{},
		transcript: &transcriptDouble{},
		seeds:      map[string]*episodeSeed{},
	}
	svc, err := privacy.New(privacy.Config{
		DB:          db,
		Media:       media,
		CoverDir:    coverDir,
		Sessions:    fx.sessions,
		Transcripts: fx.transcript,
		Owns:        ownsFunc(func(ctx context.Context, ownerID string) bool { return ownerID == fx.current }),
	})
	if err != nil {
		t.Fatalf("open privacy service: %v", err)
	}
	fx.svc = svc
	jobStore, err := jobsqlitestore.Open(t.Context(), jobsqlitestore.Config{DB: db})
	if err != nil {
		t.Fatalf("open job store: %v", err)
	}
	fx.jobStore = jobStore
	runner, err := job.Open(t.Context(), job.Config{
		Broker: stream.New(stream.Config{}),
		Store:  jobStore,
		Kinds:  map[string]job.Kind{erase.KindName: svc.Eraser().Kind()},
	})
	if err != nil {
		t.Fatalf("open job runner: %v", err)
	}
	fx.runner = runner
	if err := svc.BindRunner(runner); err != nil {
		t.Fatalf("bind runner: %v", err)
	}
	fx.seedOwner(t, "owner-a", 1)
	fx.seedOwner(t, "owner-b", 1)
	return fx
}

// as runs the next calls as owner.
func (fx *fixture) as(owner string) {
	fx.current = owner
}

// seedOwner writes one full episode: session, stems, turn, proposal
// with its decision, render, analysis, word, mention with its
// callback, cover, and the reconciliation row naming the stereo copy.
func (fx *fixture) seedOwner(t *testing.T, owner string, number int64) {
	t.Helper()
	ctx := t.Context()
	episodeID, err := id.New()
	if err != nil {
		t.Fatalf("mint episode id: %v", err)
	}
	seed := &episodeSeed{episode: episodeID, owner: owner}
	token, err := id.New()
	if err != nil {
		t.Fatalf("mint share token: %v", err)
	}
	if _, err := fx.db.Writer().ExecContext(ctx, `INSERT INTO users
		(id, kind, created_at, last_seen_at) VALUES (?, 'guest', 1, 1)`, owner); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := fx.db.Writer().ExecContext(ctx, `INSERT INTO episodes
		(id, owner_id, number, title, state, visibility, share_token, seeded)
		VALUES (?, ?, ?, ?, 'ready', 'private', ?, 0)`,
		episodeID, owner, number, "Episode for "+owner, token); err != nil {
		t.Fatalf("seed episode: %v", err)
	}
	seed.session = "prov-session-" + owner
	if _, err := fx.db.Writer().ExecContext(ctx, `INSERT INTO sessions
		(id, owner_id, episode_id, provider_session_id, token_cap, connected_seconds)
		VALUES (?, ?, ?, ?, 1800, 60)`, "sess-"+owner, owner, episodeID, seed.session); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	for _, role := range []string{"user", "host"} {
		blob := fx.persist(t, owner, episodeID, "audio/ogg", []byte("stem "+role+" "+owner))
		seed.stems = append(seed.stems, blob)
		stemID, err := id.New()
		if err != nil {
			t.Fatalf("mint stem id: %v", err)
		}
		rate := 48000
		if role == "host" {
			rate = 24000
		}
		if _, err := fx.db.Writer().ExecContext(ctx, `INSERT INTO stems
			(id, owner_id, episode_id, media_id, role, sample_rate, start_offset_ms)
			VALUES (?, ?, ?, ?, ?, ?, 0)`, stemID, owner, episodeID, blob, role, rate); err != nil {
			t.Fatalf("seed stem: %v", err)
		}
	}
	seed.opus = fx.persist(t, owner, episodeID, "audio/ogg", []byte("render opus "+owner))
	seed.aac = fx.persist(t, owner, episodeID, "audio/mp4", []byte("render aac "+owner))
	renderID, err := id.New()
	if err != nil {
		t.Fatalf("mint render id: %v", err)
	}
	if _, err := fx.db.Writer().ExecContext(ctx, `INSERT INTO renders
		(id, owner_id, episode_id, input_hash, opus_media_id, aac_media_id, loudness)
		VALUES (?, ?, ?, 'hash', ?, ?, -16)`, renderID, owner, episodeID, seed.opus, seed.aac); err != nil {
		t.Fatalf("seed render: %v", err)
	}
	seed.stereo = fx.persist(t, owner, episodeID, "audio/ogg", []byte("stereo copy "+owner))
	seed.batch = "batch-" + owner
	analysisID, err := id.New()
	if err != nil {
		t.Fatalf("mint analysis id: %v", err)
	}
	if _, err := fx.db.Writer().ExecContext(ctx, `INSERT INTO analyses
		(id, owner_id, episode_id, transcript_id, chapters, summary, entities, key_phrases)
		VALUES (?, ?, ?, ?, '', '', '', '')`, analysisID, owner, episodeID, seed.batch); err != nil {
		t.Fatalf("seed analysis: %v", err)
	}
	wordID, err := id.New()
	if err != nil {
		t.Fatalf("mint word id: %v", err)
	}
	if _, err := fx.db.Writer().ExecContext(ctx, `INSERT INTO words
		(id, owner_id, episode_id, text, start_ms, end_ms, source)
		VALUES (?, ?, ?, 'hello', 0, 120, 'rendered')`, wordID, owner, episodeID); err != nil {
		t.Fatalf("seed word: %v", err)
	}
	turnID, err := id.New()
	if err != nil {
		t.Fatalf("mint turn id: %v", err)
	}
	if _, err := fx.db.Writer().ExecContext(ctx, `INSERT INTO turns
		(id, owner_id, episode_id, role, text, started_ms, ended_ms, provider_item_id)
		VALUES (?, ?, ?, 'host', 'hello there', 0, 120, '')`, turnID, owner, episodeID); err != nil {
		t.Fatalf("seed turn: %v", err)
	}
	proposalID, err := id.New()
	if err != nil {
		t.Fatalf("mint proposal id: %v", err)
	}
	if _, err := fx.db.Writer().ExecContext(ctx, `INSERT INTO proposals
		(id, owner_id, episode_id, kind, start_word, end_word, reason)
		VALUES (?, ?, ?, 'cut', 0, 1, 'a pause')`, proposalID, owner, episodeID); err != nil {
		t.Fatalf("seed proposal: %v", err)
	}
	decisionID, err := id.New()
	if err != nil {
		t.Fatalf("mint decision id: %v", err)
	}
	if _, err := fx.db.Writer().ExecContext(ctx, `INSERT INTO decisions
		(id, owner_id, episode_id, proposal_id, decision)
		VALUES (?, ?, ?, ?, 'keep')`, decisionID, owner, episodeID, proposalID); err != nil {
		t.Fatalf("seed decision: %v", err)
	}
	mentionID, err := id.New()
	if err != nil {
		t.Fatalf("mint mention id: %v", err)
	}
	if _, err := fx.db.Writer().ExecContext(ctx, `INSERT INTO mentions
		(id, owner_id, episode_id, kind, word_offset, quote)
		VALUES (?, ?, ?, 'person', 0, 'hello')`, mentionID, owner, episodeID); err != nil {
		t.Fatalf("seed mention: %v", err)
	}
	callbackID, err := id.New()
	if err != nil {
		t.Fatalf("mint callback id: %v", err)
	}
	if _, err := fx.db.Writer().ExecContext(ctx, `INSERT INTO callbacks
		(id, owner_id, episode_id, mention_id, used)
		VALUES (?, ?, ?, ?, 0)`, callbackID, owner, episodeID, mentionID); err != nil {
		t.Fatalf("seed callback: %v", err)
	}
	seed.coverFile = episodeID + ".png"
	if err := os.WriteFile(filepath.Join(fx.coverDir, seed.coverFile), []byte("cover "+owner), 0o600); err != nil {
		t.Fatalf("seed cover: %v", err)
	}
	fx.exec(t, `CREATE TABLE IF NOT EXISTS reconcile_state (
		session_id TEXT PRIMARY KEY, recording_media_id TEXT, updated_at INTEGER)`)
	fx.exec(t, `INSERT INTO reconcile_state (session_id, recording_media_id, updated_at)
		VALUES (?, ?, 1)`, "sess-"+owner, seed.stereo)
	fx.seeds[owner] = seed
}

// persist stores bytes as a private blob grouped by episode.
func (fx *fixture) persist(t *testing.T, owner, group, contentType string, audio []byte) string {
	t.Helper()
	blob, err := fx.media.Persist(t.Context(), bytes.NewReader(audio), mediastore.Put{
		ContentType: contentType,
		Owner:       owner,
		Group:       group,
		Visibility:  mediastore.Private,
	})
	if err != nil {
		t.Fatalf("persist blob: %v", err)
	}
	return blob
}

// exec runs one write and fails the test on error.
func (fx *fixture) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	if _, err := fx.db.Writer().ExecContext(t.Context(), query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

// count returns the rows in table for owner.
func (fx *fixture) count(t *testing.T, table, owner string) int {
	t.Helper()
	var column string
	switch table {
	case "users":
		column = "id"
	case "episodes":
		column = "owner_id"
	default:
		column = "owner_id"
	}
	var total int
	if err := fx.db.Reader().QueryRowContext(t.Context(),
		"SELECT COUNT(*) FROM "+table+" WHERE "+column+" = ?", owner).Scan(&total); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return total
}

// blobVisible reads one media row visibility.
func (fx *fixture) blobVisible(t *testing.T, blob string) string {
	t.Helper()
	var visibility string
	if err := fx.db.Reader().QueryRowContext(t.Context(),
		"SELECT visibility FROM media WHERE id = ?", blob).Scan(&visibility); err != nil {
		t.Fatalf("read visibility: %v", err)
	}
	return visibility
}

// waitErased polls until the episode row is gone. The erasure deletes
// rows beside blobs and provider copies, so the row going first proves
// the fan-out reached the database.
func (fx *fixture) waitErased(t *testing.T, episodeID string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		var total int
		if err := fx.db.Reader().QueryRowContext(t.Context(),
			"SELECT COUNT(*) FROM episodes WHERE id = ?", episodeID).Scan(&total); err != nil {
			t.Fatalf("count episode: %v", err)
		}
		if total == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("episode %s never erased", episodeID)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// waitJobDone polls the job chain until the latest attempt lands. Older
// attempts may read interrupted or cancelled after a restart, so only
// the latest attempt decides. It fails past the deadline, so a stuck job
// fails loudly instead of hanging the suite.
func (fx *fixture) waitJobDone(t *testing.T, jobID string) {
	t.Helper()
	deadline := waitDeadline()
	for {
		attempts, err := fx.jobStore.Attempts(t.Context(), jobID)
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
		pastDeadline(t, deadline, "job never finished")
	}
}

// waitFirstTerminal polls the first attempt of the chain until it lands.
// The restart test stops the superseded attempt before it releases the
// provider double, so only the resumed attempt drives work after that.
func (fx *fixture) waitFirstTerminal(t *testing.T, jobID string) {
	t.Helper()
	deadline := waitDeadline()
	for {
		attempts, err := fx.jobStore.Attempts(t.Context(), jobID)
		if err != nil {
			t.Fatalf("list attempts: %v", err)
		}
		if len(attempts) > 0 {
			switch first := attempts[0]; first.Status {
			case job.StatusDone, job.StatusError, job.StatusCancelled, job.StatusInterrupted:
				return
			}
		}
		pastDeadline(t, deadline, "superseded attempt never landed")
	}
}

// waitDeadline bounds every job poll below. Doubles move on channels
// the test owns, so the deadline only fires when the code wedged.
func waitDeadline() time.Time { return time.Now().Add(15 * time.Second) }

// pastDeadline fails the test past the deadline and waits one beat
// otherwise. Every poll below shares it, so no check invents its own
// timeout.
func pastDeadline(t *testing.T, deadline time.Time, message string) {
	t.Helper()
	if time.Now().After(deadline) {
		t.Fatalf("%s", message)
	}
	time.Sleep(20 * time.Millisecond)
}
