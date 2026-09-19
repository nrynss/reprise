package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nrynss/keel/job"
	jobsqlitestore "github.com/nrynss/keel/job/sqlitestore"
	"github.com/nrynss/keel/mediastore"
	"github.com/nrynss/keel/stream"
	"github.com/nrynss/reprise/internal/broker"
	"github.com/nrynss/reprise/internal/editorial"
	"github.com/nrynss/reprise/internal/episode"
	"github.com/nrynss/reprise/internal/identity"
	"github.com/nrynss/reprise/internal/transcript"
)

// sineWAV returns one second of mono 8 kHz 16 bit sine audio. The
// schedule pins need real audio because starting a job probes the stem.
func sineWAV() []byte {
	const rate = 8000
	const freq = 440.0
	var pcm bytes.Buffer
	for i := range rate {
		v := int16(10000 * math.Sin(2*math.Pi*freq*float64(i)/rate))
		_ = binary.Write(&pcm, binary.LittleEndian, v)
	}
	raw := pcm.Bytes()
	var out bytes.Buffer
	_, _ = out.Write([]byte("RIFF"))
	_ = binary.Write(&out, binary.LittleEndian, uint32(36+len(raw)))
	_, _ = out.Write([]byte("WAVEfmt "))
	_ = binary.Write(&out, binary.LittleEndian, uint32(16))
	_ = binary.Write(&out, binary.LittleEndian, uint16(1))
	_ = binary.Write(&out, binary.LittleEndian, uint16(1))
	_ = binary.Write(&out, binary.LittleEndian, uint32(rate))
	_ = binary.Write(&out, binary.LittleEndian, uint32(rate*2))
	_ = binary.Write(&out, binary.LittleEndian, uint16(2))
	_ = binary.Write(&out, binary.LittleEndian, uint16(16))
	_, _ = out.Write([]byte("data"))
	_ = binary.Write(&out, binary.LittleEndian, uint32(len(raw)))
	_, _ = out.Write(raw)
	return out.Bytes()
}

// persistStemAudio stores one WAV stem for owner and returns its blob id.
func persistStemAudio(t *testing.T, fx *wireFixture, owner, group string, body []byte) string {
	t.Helper()
	id, err := fx.media.Persist(t.Context(), bytes.NewReader(body), mediastore.Put{
		ContentType: "audio/wav",
		Owner:       owner,
		Group:       group,
		Visibility:  mediastore.Private,
	})
	if err != nil {
		t.Fatalf("persist stem: %v", err)
	}
	return id
}

// linkStemRows stores both stem rows for one episode and fails the test
// on error.
func linkStemRows(t *testing.T, fx *wireFixture, owner, episodeID, userMedia, hostMedia string) {
	t.Helper()
	rows := []struct{ role, media string }{
		{transcript.RoleUser, userMedia},
		{transcript.RoleHost, hostMedia},
	}
	for _, row := range rows {
		if _, err := fx.db.Writer().ExecContext(t.Context(),
			`INSERT INTO stems (id, owner_id, episode_id, media_id, role, sample_rate, start_offset_ms)
			 VALUES (?, ?, ?, ?, ?, 48000, 0)`,
			"stem-"+episodeID+"-"+row.role, owner, episodeID, row.media, row.role); err != nil {
			t.Fatalf("link %s stem: %v", row.role, err)
		}
	}
}

// seedEditWords stores one edit word row for one episode and fails the
// test on error.
func seedEditWords(t *testing.T, fx *wireFixture, owner, episodeID string) {
	t.Helper()
	if _, err := fx.db.Writer().ExecContext(t.Context(),
		`INSERT INTO words (id, owner_id, episode_id, text, start_ms, end_ms, source)
		 VALUES (?, ?, ?, 'harbor', 0, 500, ?)`,
		"word-"+episodeID, owner, episodeID, transcript.SourceEdit); err != nil {
		t.Fatalf("seed edit words: %v", err)
	}
}

// openDraftJobs opens a runner with the two edit kinds over the fixture
// database and returns the draft scheduler around it.
func openDraftJobs(t *testing.T, fx *wireFixture) *jobs {
	t.Helper()
	jobStore, err := jobsqlitestore.Open(t.Context(), jobsqlitestore.Config{DB: fx.db})
	if err != nil {
		t.Fatalf("open job store: %v", err)
	}
	events := stream.New(stream.Config{})
	runner, err := job.Open(t.Context(), job.Config{
		Broker: events,
		Store:  jobStore,
		Kinds: map[string]job.Kind{
			kindEditTranscript: transcript.Kind,
			kindEditorial:      editorial.Kind,
		},
	})
	if err != nil {
		t.Fatalf("open runner: %v", err)
	}
	return &jobs{runner: runner, pipe: fx.pipe, store: jobStore}
}

// draftEpisode creates one recording episode with both stems linked and
// returns its id.
func draftEpisode(t *testing.T, fx *wireFixture, owner string) string {
	t.Helper()
	diary, err := broker.NewSQLiteDiary(fx.db)
	if err != nil {
		t.Fatalf("open diary: %v", err)
	}
	episodeID, _, err := diary.CreateEpisodeAndSession(t.Context(), owner, 1800)
	if err != nil {
		t.Fatalf("create episode: %v", err)
	}
	body := sineWAV()
	userID := persistStemAudio(t, fx, owner, episodeID, body)
	hostID := persistStemAudio(t, fx, owner, episodeID, body)
	linkStemRows(t, fx, owner, episodeID, userID, hostID)
	return episodeID
}

// TestSilentTranscriptJobNeverBlocksAnotherEpisode plants one queued
// transcript job with no episode linkage and requires a completion for
// another episode to still schedule. A silent job belongs to no episode
// the scheduler can name, so waiting on it would wedge every episode.
func TestSilentTranscriptJobNeverBlocksAnotherEpisode(t *testing.T) {
	fx := openWireFixture(t)
	const owner = "owner-silent-transcript"
	insertWireUser(t, fx, owner)
	drafts := openDraftJobs(t, fx)
	ctx := t.Context()
	silent := job.Record{
		ID: "job-silent-transcript", Kind: kindEditTranscript, Status: job.StatusQueued,
		Attempt: 1, RootID: "job-silent-transcript", UpdatedAt: time.Now(),
	}
	if err := drafts.store.Create(ctx, silent); err != nil {
		t.Fatalf("create silent job: %v", err)
	}
	episodeID := draftEpisode(t, fx, owner)
	jobID, scheduled, err := drafts.ensureTranscript(ctx, owner, episodeID)
	if err != nil {
		t.Fatalf("ensure transcript: %v", err)
	}
	if !scheduled || jobID == "" {
		t.Fatalf("scheduled = %v job %q, want a transcript start beside the silent job", scheduled, jobID)
	}
}

// TestTranscriptJobStillCoversItsOwnEpisode schedules one transcript job
// and requires a repeat for the same episode to start nothing, while
// another episode still schedules. The start stamps its episode
// synchronously, so the cover never depends on the silent window.
func TestTranscriptJobStillCoversItsOwnEpisode(t *testing.T) {
	fx := openWireFixture(t)
	const owner = "owner-transcript-cover"
	insertWireUser(t, fx, owner)
	drafts := openDraftJobs(t, fx)
	ctx := t.Context()
	first := draftEpisode(t, fx, owner)
	if _, scheduled, err := drafts.ensureTranscript(ctx, owner, first); err != nil || !scheduled {
		t.Fatalf("first ensure = %v, %v, want a transcript start", scheduled, err)
	}
	if again, covered, err := drafts.ensureTranscript(ctx, owner, first); err != nil || covered || again != "" {
		t.Fatalf("repeat ensure = %q, %v, %v, want no second start", again, covered, err)
	}
	second := draftEpisode(t, fx, owner)
	if _, scheduled, err := drafts.ensureTranscript(ctx, owner, second); err != nil || !scheduled {
		t.Fatalf("other episode ensure = %v, %v, want a transcript start", scheduled, err)
	}
}

// TestSilentEditorialJobNeverBlocksAnotherEpisode plants one queued
// editorial job with no episode linkage and requires an episode with a
// landed timeline to still schedule its editorial pass.
func TestSilentEditorialJobNeverBlocksAnotherEpisode(t *testing.T) {
	fx := openWireFixture(t)
	const owner = "owner-silent-editorial"
	insertWireUser(t, fx, owner)
	drafts := openDraftJobs(t, fx)
	ctx := t.Context()
	silent := job.Record{
		ID: "job-silent-editorial", Kind: kindEditorial, Status: job.StatusQueued,
		Attempt: 1, RootID: "job-silent-editorial", UpdatedAt: time.Now(),
	}
	if err := drafts.store.Create(ctx, silent); err != nil {
		t.Fatalf("create silent job: %v", err)
	}
	episodeID := draftEpisode(t, fx, owner)
	seedEditWords(t, fx, owner, episodeID)
	jobID, scheduled, err := drafts.ensureEditorial(ctx, owner, episodeID)
	if err != nil {
		t.Fatalf("ensure editorial: %v", err)
	}
	if !scheduled || jobID == "" {
		t.Fatalf("scheduled = %v job %q, want an editorial start beside the silent job", scheduled, jobID)
	}
}

// TestDescribedEditorialJobStillCoversItsEpisode plants one running
// editorial job described for an episode and requires the schedule for
// that episode to start nothing.
func TestDescribedEditorialJobStillCoversItsEpisode(t *testing.T) {
	fx := openWireFixture(t)
	const owner = "owner-editorial-cover"
	insertWireUser(t, fx, owner)
	drafts := openDraftJobs(t, fx)
	ctx := t.Context()
	episodeID := draftEpisode(t, fx, owner)
	seedEditWords(t, fx, owner, episodeID)
	detail, err := json.Marshal(episodeDescriptor{OwnerID: owner, EpisodeID: episodeID})
	if err != nil {
		t.Fatalf("encode descriptor: %v", err)
	}
	covering := job.Record{
		ID: "job-covering-editorial", Kind: kindEditorial, Status: job.StatusRunning,
		Attempt: 1, RootID: "job-covering-editorial",
		Progress:  job.Progress{Stage: "start", Detail: detail},
		UpdatedAt: time.Now(),
	}
	if err := drafts.store.Create(ctx, covering); err != nil {
		t.Fatalf("create covering job: %v", err)
	}
	if again, covered, err := drafts.ensureEditorial(ctx, owner, episodeID); err != nil || covered || again != "" {
		t.Fatalf("covered ensure = %q, %v, %v, want no second start", again, covered, err)
	}
}

// waitDescribed waits until every named job carries episode linkage and
// fails the test when one stays silent. The poll only awaits async job
// reports and asserts nothing about timing.
func waitDescribed(t *testing.T, store job.Store, ids ...string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for _, id := range ids {
		for {
			rec, err := store.Get(t.Context(), id)
			if err != nil {
				t.Fatalf("read job %s: %v", id, err)
			}
			if len(rec.Progress.Detail) > 0 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("job %s stayed silent", id)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
}

// TestFullEditorialQueueAnswersRetry fills the editorial kind with two
// described blocking jobs for other episodes, then completes stems for
// an episode with a landed timeline. The completion must answer 429
// with the retry code, the same answer a full transcript queue gives.
func TestFullEditorialQueueAnswersRetry(t *testing.T) {
	fx := openWireFixture(t)
	sessionBroker := openWireBroker(t, fx)
	identitySvc, err := identity.New(t.Context(), identity.Config{DB: fx.db, SigningKey: "wire-test-signing-key"})
	if err != nil {
		t.Fatalf("open identity: %v", err)
	}
	session, cookie := mintWireSession(t, fx, sessionBroker)
	if cookie == nil {
		t.Fatal("mint set no guest cookie")
	}
	var owner string
	if err := fx.db.Reader().QueryRowContext(t.Context(),
		`SELECT owner_id FROM sessions WHERE id = ?`, session.SessionID).Scan(&owner); err != nil {
		t.Fatalf("read session owner: %v", err)
	}
	ctx := t.Context()
	diary, err := broker.NewSQLiteDiary(fx.db)
	if err != nil {
		t.Fatalf("open diary: %v", err)
	}
	episodeID, _, err := diary.CreateEpisodeAndSession(ctx, owner, 1800)
	if err != nil {
		t.Fatalf("create episode: %v", err)
	}
	body := sineWAV()
	userID := persistStemAudio(t, fx, owner, episodeID, body)
	hostID := persistStemAudio(t, fx, owner, episodeID, body)
	linkStemRows(t, fx, owner, episodeID, userID, hostID)
	seedEditWords(t, fx, owner, episodeID)
	episodeSvc, err := episode.NewService(episode.Config{DB: fx.db})
	if err != nil {
		t.Fatalf("new episode service: %v", err)
	}
	drafts := openDraftJobs(t, fx)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	block := func(other string) job.Func {
		detail, err := json.Marshal(episodeDescriptor{OwnerID: owner, EpisodeID: other})
		if err != nil {
			t.Fatalf("encode descriptor: %v", err)
		}
		return func(ctx context.Context, progress func(job.Progress)) ([]byte, error) {
			progress(job.Progress{Stage: "start", Detail: detail})
			select {
			case <-release:
				return []byte("{}"), nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}
	var blockers []string
	for _, other := range []string{"episode-blocker-a", "episode-blocker-b"} {
		id, err := drafts.runner.StartKind(ctx, kindEditorial, block(other))
		if err != nil {
			t.Fatalf("start blocker: %v", err)
		}
		blockers = append(blockers, id)
	}
	waitDescribed(t, drafts.store, blockers...)
	handler := identitySvc.Middleware(newStemsComplete(episodeSvc, drafts, fx.db))
	complete, err := json.Marshal(stemsCompleteRequest{
		UserMediaID: userID, HostMediaID: hostID,
		UserSampleRate: 48000, HostSampleRate: 48000,
	})
	if err != nil {
		t.Fatalf("encode completion: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/episodes/"+episodeID+"/stems/complete", bytes.NewReader(complete))
	req.AddCookie(cookie)
	req.SetPathValue("id", episodeID)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), codePipelineBusy) {
		t.Fatalf("body = %s, want the retry code", rec.Body.String())
	}
}
