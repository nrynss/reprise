package episode_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/nrynss/keel/job"
	jobsqlitestore "github.com/nrynss/keel/job/sqlitestore"
	"github.com/nrynss/keel/sqlite"
	"github.com/nrynss/reprise/internal/episode"
)

// openJobStore creates the durable job rows on a diary database and fails
// the test on error.
func openJobStore(t *testing.T, db *sqlite.DB) *jobsqlitestore.Store {
	t.Helper()
	store, err := jobsqlitestore.Open(t.Context(), jobsqlitestore.Config{DB: db})
	if err != nil {
		t.Fatalf("open job store: %v", err)
	}
	return store
}

// seedJobRow stores one job record stamped for an episode and fails the
// test on error.
func seedJobRow(t *testing.T, store *jobsqlitestore.Store, id, kind, ownerID, episodeID string, status job.Status, errText string, updatedAt time.Time) {
	t.Helper()
	detail, err := json.Marshal(map[string]string{"owner_id": ownerID, "episode_id": episodeID})
	if err != nil {
		t.Fatalf("encode linkage: %v", err)
	}
	rec := job.Record{
		ID:        id,
		Kind:      kind,
		Status:    status,
		Attempt:   1,
		RootID:    id,
		Progress:  job.Progress{Stage: "start", Detail: detail},
		UpdatedAt: updatedAt,
	}
	if errText != "" {
		rec.Err = errors.New(errText)
	}
	if err := store.Create(t.Context(), rec); err != nil {
		t.Fatalf("create job %s: %v", id, err)
	}
}

// outcomeService returns a service naming the transcript kind, and fails
// the test when construction breaks.
func outcomeService(t *testing.T, db *sqlite.DB) *episode.Service {
	t.Helper()
	svc, err := episode.NewService(episode.Config{DB: db, TranscriptKind: "edit_transcript"})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	return svc
}

// TestTranscriptOutcomeReadsLatestPass stores an error then a done pass
// for one episode and requires the latest outcome with its state, id,
// and failure text.
func TestTranscriptOutcomeReadsLatestPass(t *testing.T) {
	t.Parallel()
	db := openDatabase(t)
	store := openJobStore(t, db)
	beginEpisode(t, db, "ep-1", 1)
	now := time.Now()
	seedJobRow(t, store, "job-old", "edit_transcript", "owner-1", "ep-1", job.StatusError, "provider refused", now.Add(-time.Minute))
	seedJobRow(t, store, "job-new", "edit_transcript", "owner-1", "ep-1", job.StatusDone, "", now)
	got, err := outcomeService(t, db).TranscriptOutcome(t.Context(), "owner-1", "ep-1")
	if err != nil {
		t.Fatalf("transcript outcome: %v", err)
	}
	if !got.Found || got.JobID != "job-new" || got.Status != string(job.StatusDone) || got.Error != "" {
		t.Fatalf("outcome = %+v, want the done job-new with no error", got)
	}
}

// TestTranscriptOutcomeReportsFailure stores one failed pass and requires
// its id, state, and error text to surface.
func TestTranscriptOutcomeReportsFailure(t *testing.T) {
	t.Parallel()
	db := openDatabase(t)
	store := openJobStore(t, db)
	beginEpisode(t, db, "ep-1", 1)
	seedJobRow(t, store, "job-1", "edit_transcript", "owner-1", "ep-1", job.StatusError, "provider refused", time.Now())
	got, err := outcomeService(t, db).TranscriptOutcome(t.Context(), "owner-1", "ep-1")
	if err != nil {
		t.Fatalf("transcript outcome: %v", err)
	}
	if !got.Found || got.JobID != "job-1" || got.Status != string(job.StatusError) || got.Error != "provider refused" {
		t.Fatalf("outcome = %+v, want job-1 error with its text", got)
	}
}

// TestTranscriptOutcomeIgnoresUnrelatedRows stores a silent job, a job
// for another episode, and a job of another kind, and requires no match.
func TestTranscriptOutcomeIgnoresUnrelatedRows(t *testing.T) {
	t.Parallel()
	db := openDatabase(t)
	store := openJobStore(t, db)
	beginEpisode(t, db, "ep-1", 1)
	ctx := t.Context()
	silent := job.Record{
		ID: "job-silent", Kind: "edit_transcript", Status: job.StatusQueued,
		Attempt: 1, RootID: "job-silent", UpdatedAt: time.Now(),
	}
	if err := store.Create(ctx, silent); err != nil {
		t.Fatalf("create silent job: %v", err)
	}
	seedJobRow(t, store, "job-other-episode", "edit_transcript", "owner-1", "ep-9", job.StatusDone, "", time.Now())
	seedJobRow(t, store, "job-other-kind", "editorial", "owner-1", "ep-1", job.StatusDone, "", time.Now())
	got, err := outcomeService(t, db).TranscriptOutcome(ctx, "owner-1", "ep-1")
	if err != nil {
		t.Fatalf("transcript outcome: %v", err)
	}
	if got.Found {
		t.Fatalf("outcome = %+v, want no pass for ep-1", got)
	}
}

// TestTranscriptOutcomeNeedsKindAndOwnership requires a service without
// a kind to report no pass, and foreign or missing episodes to refuse.
func TestTranscriptOutcomeNeedsKindAndOwnership(t *testing.T) {
	t.Parallel()
	db := openDatabase(t)
	store := openJobStore(t, db)
	seedOwner(t, db, "owner-2")
	beginEpisode(t, db, "ep-1", 1)
	seedJobRow(t, store, "job-1", "edit_transcript", "owner-1", "ep-1", job.StatusDone, "", time.Now())
	bare, err := episode.NewService(episode.Config{DB: db})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	got, err := bare.TranscriptOutcome(t.Context(), "owner-1", "ep-1")
	if err != nil {
		t.Fatalf("bare outcome: %v", err)
	}
	if got.Found {
		t.Fatalf("bare outcome = %+v, want no pass without a kind", got)
	}
	svc := outcomeService(t, db)
	if _, err := svc.TranscriptOutcome(t.Context(), "owner-2", "ep-1"); !errors.Is(err, episode.ErrNotFound) {
		t.Fatalf("foreign error = %v, want not found", err)
	}
	if _, err := svc.TranscriptOutcome(t.Context(), "owner-1", "missing"); !errors.Is(err, episode.ErrNotFound) {
		t.Fatalf("missing error = %v, want not found", err)
	}
	if _, err := svc.TranscriptOutcome(t.Context(), "", "ep-1"); !errors.Is(err, episode.ErrInvalid) {
		t.Fatalf("empty owner error = %v, want invalid", err)
	}
}
