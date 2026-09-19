package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nrynss/keel/job"
	jobsqlitestore "github.com/nrynss/keel/job/sqlitestore"
	"github.com/nrynss/keel/sqlite"
	"github.com/nrynss/reprise/internal/episode"
)

// openJobRows creates the durable job rows on a diary database and fails
// the test on error.
func openJobRows(t *testing.T, db *sqlite.DB) *jobsqlitestore.Store {
	t.Helper()
	store, err := jobsqlitestore.Open(t.Context(), jobsqlitestore.Config{DB: db})
	if err != nil {
		t.Fatalf("open job store: %v", err)
	}
	return store
}

// seedPassRow stores one transcript pass stamped for an episode and fails
// the test on error.
func seedPassRow(t *testing.T, store *jobsqlitestore.Store, id, owner, episodeID string, status job.Status, errText string) {
	t.Helper()
	detail, err := json.Marshal(map[string]string{"owner_id": owner, "episode_id": episodeID})
	if err != nil {
		t.Fatalf("encode linkage: %v", err)
	}
	rec := job.Record{
		ID:        id,
		Kind:      "edit_transcript",
		Status:    status,
		Attempt:   1,
		RootID:    id,
		Progress:  job.Progress{Stage: "start", Detail: detail},
		UpdatedAt: time.Now(),
	}
	if errText != "" {
		rec.Err = errors.New(errText)
	}
	if err := store.Create(t.Context(), rec); err != nil {
		t.Fatalf("create pass %s: %v", id, err)
	}
}

// outcomeService wires the real episode service naming the transcript
// kind, so the detail carries the pass outcome.
func outcomeService(t *testing.T, db *sqlite.DB) *episode.Service {
	t.Helper()
	svc, err := episode.NewService(episode.Config{DB: db, TranscriptKind: "edit_transcript"})
	if err != nil {
		t.Fatalf("new episode service: %v", err)
	}
	return svc
}

// TestEpisodeDetailCarriesPassOutcome stores a failed transcript pass
// and requires the detail to carry its id, state, and error beside the
// proposals.
func TestEpisodeDetailCarriesPassOutcome(t *testing.T) {
	t.Parallel()
	db, guests := openDiary(t)
	cookie, owner := mintGuest(t, guests)
	seedEpisodeRow(t, db, "ep-1", owner.ID, 1, "failed")
	seedProposalRow(t, db, "cut-1", owner.ID, "ep-1")
	seedPassRow(t, openJobRows(t, db), "job-1", owner.ID, "ep-1", job.StatusError, "provider refused")

	rec := serve(guests, NewEpisodes(outcomeService(t, db)), cookie,
		httptest.NewRequest(http.MethodGet, "/api/episodes/ep-1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("detail status = %d, want 200", rec.Code)
	}
	var body episodeDetailJSON
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if len(body.Proposals) != 1 {
		t.Fatalf("proposals = %+v, want the stored cut", body.Proposals)
	}
	if body.TranscriptOutcome == nil {
		t.Fatal("transcript outcome is missing, want the failed pass")
	}
	got := body.TranscriptOutcome
	if got.JobID != "job-1" || got.Status != string(job.StatusError) || got.Error != "provider refused" {
		t.Fatalf("outcome = %+v, want job-1 error with its text", got)
	}
}

// TestEpisodeDetailOmitsPassOutcomeWithoutPass requires the detail to
// carry no outcome when no pass ever started.
func TestEpisodeDetailOmitsPassOutcomeWithoutPass(t *testing.T) {
	t.Parallel()
	db, guests := openDiary(t)
	cookie, owner := mintGuest(t, guests)
	seedEpisodeRow(t, db, "ep-1", owner.ID, 1, "draft")
	openJobRows(t, db)

	rec := serve(guests, NewEpisodes(outcomeService(t, db)), cookie,
		httptest.NewRequest(http.MethodGet, "/api/episodes/ep-1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("detail status = %d, want 200", rec.Code)
	}
	var body episodeDetailJSON
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if body.TranscriptOutcome != nil {
		t.Fatalf("outcome = %+v, want no pass outcome", body.TranscriptOutcome)
	}
}
