package episode_test

import (
	"context"
	"errors"
	"testing"

	"github.com/nrynss/keel/job"
	"github.com/nrynss/keel/sqlite"
	"github.com/nrynss/reprise/internal/episode"
)

// errFakeFull is the starter fault the failure test binds.
var errFakeFull = errors.New("fake starter is full")

// fakeStarter records the kind it started and answers a fixed id or a
// fixed error, so the mark done path runs with no runner.
type fakeStarter struct {
	calls int
	kind  string
	id    string
	err   error
}

// StartKind records the call and answers the fixed outcome.
func (f *fakeStarter) StartKind(_ context.Context, kind string, _ job.Func) (string, error) {
	f.calls++
	f.kind = kind
	if f.err != nil {
		return "", f.err
	}
	return f.id, nil
}

// fakeRender builds a no-op render func, so the starter receives work it
// never runs.
type fakeRender struct{}

// Func returns work that answers an empty object.
func (fakeRender) Func(_, _ string) job.Func {
	return func(_ context.Context, _ func(job.Progress)) ([]byte, error) {
		return []byte("{}"), nil
	}
}

// mustService returns a service with a working starter, and fails the
// test when construction breaks.
func mustService(t *testing.T, db *sqlite.DB, starter *fakeStarter) *episode.Service {
	t.Helper()
	svc, err := episode.NewService(episode.Config{
		DB:         db,
		Starter:    starter,
		RenderKind: "render",
		Render:     fakeRender{},
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	return svc
}

// seedOwner inserts one extra user row and fails the test on error.
func seedOwner(t *testing.T, db *sqlite.DB, ownerID string) {
	t.Helper()
	if _, err := db.Writer().ExecContext(t.Context(),
		"INSERT INTO users (id, kind, created_at, last_seen_at) VALUES (?, 'guest', 1, 2)", ownerID); err != nil {
		t.Fatalf("seed owner %s: %v", ownerID, err)
	}
}

// seedSession inserts one session row on an episode and fails the test on
// error.
func seedSession(t *testing.T, db *sqlite.DB, sessionID, ownerID, episodeID string) {
	t.Helper()
	if _, err := db.Writer().ExecContext(t.Context(),
		`INSERT INTO sessions (id, owner_id, episode_id, provider_session_id, token_cap, connected_seconds)
		 VALUES (?, ?, ?, '', 1800, 0)`, sessionID, ownerID, episodeID); err != nil {
		t.Fatalf("seed session %s: %v", sessionID, err)
	}
}

// seedProposal inserts one proposal and fails the test on error.
func seedProposal(t *testing.T, db *sqlite.DB, proposalID, ownerID, episodeID, kind string) {
	t.Helper()
	if _, err := db.Writer().ExecContext(t.Context(),
		`INSERT INTO proposals (id, owner_id, episode_id, kind, start_word, end_word, reason)
		 VALUES (?, ?, ?, ?, 0, 1, 'test')`, proposalID, ownerID, episodeID, kind); err != nil {
		t.Fatalf("seed proposal %s: %v", proposalID, err)
	}
}

// TestNewServiceRefusesNilDatabase checks construction needs a database.
func TestNewServiceRefusesNilDatabase(t *testing.T) {
	t.Parallel()
	if _, err := episode.NewService(episode.Config{}); !errors.Is(err, episode.ErrInvalid) {
		t.Fatalf("new service error = %v, want ErrInvalid", err)
	}
}

// TestListScopesOnOwner lists two owners and requires each to see only
// its own episodes, oldest number first.
func TestListScopesOnOwner(t *testing.T) {
	t.Parallel()
	db := openDatabase(t)
	seedOwner(t, db, "owner-2")
	beginEpisode(t, db, "ep-1", 2)
	beginEpisode(t, db, "ep-2", 1)
	if err := episode.Begin(t.Context(), db, episode.NewEpisode{
		ID: "ep-3", OwnerID: "owner-2", Number: 1, Title: "Episode ep-3",
	}); err != nil {
		t.Fatalf("begin ep-3: %v", err)
	}
	svc, err := episode.NewService(episode.Config{DB: db})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	got, err := svc.List(t.Context(), "owner-1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 || got[0].ID != "ep-2" || got[1].ID != "ep-1" {
		t.Fatalf("list = %+v, want ep-2 then ep-1", got)
	}
	other, err := svc.List(t.Context(), "owner-2")
	if err != nil {
		t.Fatalf("list owner-2: %v", err)
	}
	if len(other) != 1 || other[0].ID != "ep-3" {
		t.Fatalf("list owner-2 = %+v, want only ep-3", other)
	}
	if _, err := svc.List(t.Context(), ""); !errors.Is(err, episode.ErrInvalid) {
		t.Fatalf("list empty owner error = %v, want ErrInvalid", err)
	}
}

// TestGetHidesForeignEpisodes requires unknown and foreign ids to report
// not found alike.
func TestGetHidesForeignEpisodes(t *testing.T) {
	t.Parallel()
	db := openDatabase(t)
	seedOwner(t, db, "owner-2")
	beginEpisode(t, db, "ep-1", 1)
	svc, err := episode.NewService(episode.Config{DB: db})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	got, err := svc.Get(t.Context(), "owner-1", "ep-1")
	if err != nil {
		t.Fatalf("get own: %v", err)
	}
	if got.ID != "ep-1" || got.OwnerID != "owner-1" || got.Number != 1 {
		t.Fatalf("get own = %+v, want ep-1 of owner-1 number 1", got)
	}
	if _, err := svc.Get(t.Context(), "owner-2", "ep-1"); !errors.Is(err, episode.ErrNotFound) {
		t.Fatalf("get foreign error = %v, want ErrNotFound", err)
	}
	if _, err := svc.Get(t.Context(), "owner-1", "missing"); !errors.Is(err, episode.ErrNotFound) {
		t.Fatalf("get missing error = %v, want ErrNotFound", err)
	}
}

// TestDecideAppendsLatestWins accepts then reverts one proposal and
// requires the latest row to win on the read back.
func TestDecideAppendsLatestWins(t *testing.T) {
	t.Parallel()
	db := openDatabase(t)
	plantEpisode(t, db, "ep-1", 1, episode.StateDraft)
	seedProposal(t, db, "cut-1", "owner-1", "ep-1", "cut")
	svc, err := episode.NewService(episode.Config{DB: db})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	if err := svc.Decide(t.Context(), "owner-1", "ep-1", "cut-1", episode.DecisionAccepted); err != nil {
		t.Fatalf("accept: %v", err)
	}
	if err := svc.Decide(t.Context(), "owner-1", "ep-1", "cut-1", episode.DecisionReverted); err != nil {
		t.Fatalf("revert: %v", err)
	}
	got, err := svc.Proposals(t.Context(), "owner-1", "ep-1")
	if err != nil {
		t.Fatalf("proposals: %v", err)
	}
	if len(got) != 1 || !got[0].HasDecision || got[0].Decision != episode.DecisionReverted {
		t.Fatalf("proposals = %+v, want one reverted cut", got)
	}
}

// TestDecideRefusals checks bad values, unknown proposals, and foreign
// episodes each fail with the right sentinel.
func TestDecideRefusals(t *testing.T) {
	t.Parallel()
	db := openDatabase(t)
	seedOwner(t, db, "owner-2")
	plantEpisode(t, db, "ep-1", 1, episode.StateDraft)
	seedProposal(t, db, "cut-1", "owner-1", "ep-1", "cut")
	svc, err := episode.NewService(episode.Config{DB: db})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	if err := svc.Decide(t.Context(), "owner-1", "ep-1", "cut-1", "maybe"); !errors.Is(err, episode.ErrInvalid) {
		t.Fatalf("bad value error = %v, want ErrInvalid", err)
	}
	if err := svc.Decide(t.Context(), "owner-1", "ep-1", "missing", episode.DecisionAccepted); !errors.Is(err, episode.ErrNotFound) {
		t.Fatalf("unknown proposal error = %v, want ErrNotFound", err)
	}
	if err := svc.Decide(t.Context(), "owner-2", "ep-1", "cut-1", episode.DecisionAccepted); !errors.Is(err, episode.ErrNotFound) {
		t.Fatalf("foreign episode error = %v, want ErrNotFound", err)
	}
	if _, err := svc.Proposals(t.Context(), "owner-2", "ep-1"); !errors.Is(err, episode.ErrNotFound) {
		t.Fatalf("foreign proposals error = %v, want ErrNotFound", err)
	}
}

// TestRequestRenderStartsOneJob marks a draft done and requires the
// starter to run once under its kind, with a repeat tap refused.
func TestRequestRenderStartsOneJob(t *testing.T) {
	t.Parallel()
	db := openDatabase(t)
	plantEpisode(t, db, "ep-1", 1, episode.StateDraft)
	starter := &fakeStarter{id: "job-1"}
	svc := mustService(t, db, starter)
	jobID, err := svc.RequestRender(t.Context(), "owner-1", "ep-1")
	if err != nil {
		t.Fatalf("request render: %v", err)
	}
	if jobID != "job-1" {
		t.Fatalf("job id = %q, want job-1", jobID)
	}
	if starter.calls != 1 || starter.kind != "render" {
		t.Fatalf("starter calls = %d kind = %q, want 1 call of render", starter.calls, starter.kind)
	}
	if got := stateOf(t, db, "ep-1"); got != episode.StateRendering {
		t.Fatalf("state = %s, want rendering", got)
	}
	if _, err := svc.RequestRender(t.Context(), "owner-1", "ep-1"); !errors.Is(err, episode.ErrIllegalTransition) {
		t.Fatalf("second request error = %v, want ErrIllegalTransition", err)
	}
	if starter.calls != 1 {
		t.Fatalf("starter calls = %d, want no second start", starter.calls)
	}
}

// TestRequestRenderFailsEpisodeWhenJobNeverStarts requires a starter
// fault to fail the episode and report the start, so a retry can resume.
func TestRequestRenderFailsEpisodeWhenJobNeverStarts(t *testing.T) {
	t.Parallel()
	db := openDatabase(t)
	plantEpisode(t, db, "ep-1", 1, episode.StateDraft)
	starter := &fakeStarter{err: errFakeFull}
	svc := mustService(t, db, starter)
	if _, err := svc.RequestRender(t.Context(), "owner-1", "ep-1"); !errors.Is(err, episode.ErrStart) {
		t.Fatalf("request render error = %v, want ErrStart", err)
	}
	if got := stateOf(t, db, "ep-1"); got != episode.StateFailed {
		t.Fatalf("state = %s, want failed", got)
	}
}

// TestRequestRenderRefusals checks missing runners, foreign episodes,
// and non draft states each fail before any job starts.
func TestRequestRenderRefusals(t *testing.T) {
	t.Parallel()
	db := openDatabase(t)
	seedOwner(t, db, "owner-2")
	plantEpisode(t, db, "ep-1", 1, episode.StateDraft)
	plantEpisode(t, db, "ep-2", 2, episode.StateRecording)
	bare, err := episode.NewService(episode.Config{DB: db})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	if _, err := bare.RequestRender(t.Context(), "owner-1", "ep-1"); !errors.Is(err, episode.ErrInvalid) {
		t.Fatalf("no runner error = %v, want ErrInvalid", err)
	}
	svc := mustService(t, db, &fakeStarter{id: "job-1"})
	if _, err := svc.RequestRender(t.Context(), "owner-2", "ep-1"); !errors.Is(err, episode.ErrNotFound) {
		t.Fatalf("foreign error = %v, want ErrNotFound", err)
	}
	if _, err := svc.RequestRender(t.Context(), "owner-1", "ep-2"); !errors.Is(err, episode.ErrIllegalTransition) {
		t.Fatalf("recording error = %v, want ErrIllegalTransition", err)
	}
	if got := stateOf(t, db, "ep-2"); got != episode.StateRecording {
		t.Fatalf("state = %s, want refused moves to leave recording", got)
	}
}

// TestRecordSessionEndStoresProviderClose records the provider id, reads
// it back from the row, and repeats the same end harmlessly.
func TestRecordSessionEndStoresProviderClose(t *testing.T) {
	t.Parallel()
	db := openDatabase(t)
	seedOwner(t, db, "owner-2")
	beginEpisode(t, db, "ep-1", 1)
	seedSession(t, db, "sess-1", "owner-1", "ep-1")
	svc, err := episode.NewService(episode.Config{DB: db})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	episodeID, err := svc.RecordSessionEnd(t.Context(), "owner-1", "sess-1", "prov-9")
	if err != nil {
		t.Fatalf("record end: %v", err)
	}
	if episodeID != "ep-1" {
		t.Fatalf("episode id = %q, want ep-1", episodeID)
	}
	var stored string
	if err := db.Reader().QueryRowContext(t.Context(),
		"SELECT provider_session_id FROM sessions WHERE id = 'sess-1'").Scan(&stored); err != nil {
		t.Fatalf("read provider id: %v", err)
	}
	if stored != "prov-9" {
		t.Fatalf("provider id = %q, want prov-9", stored)
	}
	if _, err := svc.RecordSessionEnd(t.Context(), "owner-1", "sess-1", "prov-9"); err != nil {
		t.Fatalf("repeat end: %v", err)
	}
	if _, err := svc.RecordSessionEnd(t.Context(), "owner-2", "sess-1", "prov-9"); !errors.Is(err, episode.ErrNotFound) {
		t.Fatalf("foreign end error = %v, want ErrNotFound", err)
	}
	if _, err := svc.RecordSessionEnd(t.Context(), "owner-1", "missing", "prov-9"); !errors.Is(err, episode.ErrNotFound) {
		t.Fatalf("missing end error = %v, want ErrNotFound", err)
	}
}
