package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
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
	if body.EditorialOutcome != nil {
		t.Fatalf("editorial outcome = %+v, want none", body.EditorialOutcome)
	}
}

// TestEpisodeDetailCarriesEditorialOutcome stores a done transcript pass
// and a failed editorial pass on a draft with no proposals. The detail
// must carry both, so the gallery can say the editorial pass failed.
func TestEpisodeDetailCarriesEditorialOutcome(t *testing.T) {
	t.Parallel()
	db, guests := openDiary(t)
	cookie, owner := mintGuest(t, guests)
	seedEpisodeRow(t, db, "ep-1", owner.ID, 1, "draft")
	jobs := openJobRows(t, db)
	seedPassRow(t, jobs, "job-words", owner.ID, "ep-1", job.StatusDone, "")
	detail, err := json.Marshal(map[string]string{"owner_id": owner.ID, "episode_id": "ep-1"})
	if err != nil {
		t.Fatalf("encode linkage: %v", err)
	}
	if err := jobs.Create(t.Context(), job.Record{
		ID:        "job-edit",
		Kind:      episode.EditorialKind,
		Status:    job.StatusError,
		Attempt:   1,
		RootID:    "job-edit",
		Progress:  job.Progress{Stage: "start", Detail: detail},
		UpdatedAt: time.Now(),
		Err:       errors.New("budget refused"),
	}); err != nil {
		t.Fatalf("create editorial pass: %v", err)
	}

	rec := serve(guests, NewEpisodes(outcomeService(t, db)), cookie,
		httptest.NewRequest(http.MethodGet, "/api/episodes/ep-1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("detail status = %d, want 200", rec.Code)
	}
	var body episodeDetailJSON
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if body.TranscriptOutcome == nil || body.TranscriptOutcome.JobID != "job-words" {
		t.Fatalf("transcript outcome = %+v, want the done job-words", body.TranscriptOutcome)
	}
	got := body.EditorialOutcome
	if got == nil || got.JobID != "job-edit" || got.Status != string(job.StatusError) || got.Error != "budget refused" {
		t.Fatalf("editorial outcome = %+v, want the failed job-edit with its text", got)
	}
}

// seedKindRow stores one job of a named kind stamped for an episode and
// fails the test on error.
func seedKindRow(t *testing.T, store *jobsqlitestore.Store, id, kind, owner, episodeID string, status job.Status, errText string, updatedAt time.Time) {
	t.Helper()
	detail, err := json.Marshal(map[string]string{"owner_id": owner, "episode_id": episodeID, "render_id": "render-1"})
	if err != nil {
		t.Fatalf("encode linkage: %v", err)
	}
	rec := job.Record{
		ID: id, Kind: kind, Status: status, Attempt: 1, RootID: id,
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

// TestEpisodeDetailCarriesRenderPassOutcomes stores a done render, a
// failed analysis, and a running marking pass for an analysing episode.
// The detail names each pass with its state, and the failure text rides
// on the analysis pass alone. Another owner reads 404 and none of it.
func TestEpisodeDetailCarriesRenderPassOutcomes(t *testing.T) {
	t.Parallel()
	db, guests := openDiary(t)
	cookie, owner := mintGuest(t, guests)
	seedEpisodeRow(t, db, "ep-1", owner.ID, 1, "analysing")
	store := openJobRows(t, db)
	now := time.Now()
	seedKindRow(t, store, "job-render", "render", owner.ID, "ep-1", job.StatusDone, "", now.Add(-2*time.Minute))
	seedKindRow(t, store, "job-analysis", episode.AnalysisKind, owner.ID, "ep-1", job.StatusError, "batch refused", now.Add(-time.Minute))
	seedKindRow(t, store, "job-memory", episode.MemoryKind, owner.ID, "ep-1", job.StatusRunning, "", now)
	handler := NewEpisodes(newEpisodeService(t, db, nil).svc)

	rec := serve(guests, handler, cookie, httptest.NewRequest(http.MethodGet, "/api/episodes/ep-1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("detail status = %d, want 200", rec.Code)
	}
	var body episodeDetailJSON
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if body.RenderOutcome == nil || body.RenderOutcome.JobID != "job-render" || body.RenderOutcome.Status != "done" {
		t.Fatalf("render outcome = %+v, want the done job-render", body.RenderOutcome)
	}
	if body.AnalysisOutcome == nil || body.AnalysisOutcome.JobID != "job-analysis" ||
		body.AnalysisOutcome.Status != "error" || body.AnalysisOutcome.Error != "batch refused" {
		t.Fatalf("analysis outcome = %+v, want the failed job-analysis with its text", body.AnalysisOutcome)
	}
	if body.MemoryOutcome == nil || body.MemoryOutcome.JobID != "job-memory" || body.MemoryOutcome.Status != "running" {
		t.Fatalf("memory outcome = %+v, want the running job-memory", body.MemoryOutcome)
	}

	strangerCookie, _ := mintGuest(t, guests)
	foreign := serve(guests, handler, strangerCookie, httptest.NewRequest(http.MethodGet, "/api/episodes/ep-1", nil))
	if foreign.Code != http.StatusNotFound {
		t.Fatalf("stranger status = %d, want 404", foreign.Code)
	}
	if strings.Contains(foreign.Body.String(), "job-render") || strings.Contains(foreign.Body.String(), "batch refused") {
		t.Fatalf("stranger body leaks the passes: %s", foreign.Body.String())
	}
}
