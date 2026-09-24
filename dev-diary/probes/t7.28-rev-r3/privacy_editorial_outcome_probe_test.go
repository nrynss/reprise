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
	"github.com/nrynss/reprise/internal/episode"
)

// TestProbeForeignEditorialOutcomeLeaksNothing is a review probe. The
// first owner holds a draft whose editorial pass failed with a private
// reason. A second owner asking for that detail gets 404 and neither
// the job id nor the reason. The second owner's own detail carries no
// editorial outcome at all. An anonymous request is refused.
func TestProbeForeignEditorialOutcomeLeaksNothing(t *testing.T) {
	db, guests := openDiary(t)
	ownerCookie, owner := mintGuest(t, guests)
	otherCookie, other := mintGuest(t, guests)
	seedEpisodeRow(t, db, "ep-1", owner.ID, 1, "draft")
	seedEpisodeRow(t, db, "ep-2", other.ID, 1, "draft")
	jobs := openJobRows(t, db)
	seedPassRow(t, jobs, "job-words", owner.ID, "ep-1", job.StatusDone, "")
	linkage, err := json.Marshal(map[string]string{"owner_id": owner.ID, "episode_id": "ep-1"})
	if err != nil {
		t.Fatalf("encode linkage: %v", err)
	}
	if err := jobs.Create(t.Context(), job.Record{
		ID: "job-secret-edit", Kind: episode.EditorialKind, Status: job.StatusError,
		Attempt: 1, RootID: "job-secret-edit",
		Progress:  job.Progress{Stage: "start", Detail: linkage},
		UpdatedAt: time.Now(), Err: errors.New("secretreason"),
	}); err != nil {
		t.Fatalf("create editorial pass: %v", err)
	}
	handler := NewEpisodes(outcomeService(t, db))

	rec := serve(guests, handler, otherCookie, httptest.NewRequest(http.MethodGet, "/api/episodes/ep-1", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("foreign status = %d, want 404", rec.Code)
	}
	for _, leak := range []string{"job-secret-edit", "secretreason", "editorial_outcome"} {
		if strings.Contains(rec.Body.String(), leak) {
			t.Fatalf("foreign body leaks %q: %s", leak, rec.Body.String())
		}
	}
	rec = serve(guests, handler, otherCookie, httptest.NewRequest(http.MethodGet, "/api/episodes/ep-2", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("own status = %d, want 200", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "editorial_outcome") || strings.Contains(rec.Body.String(), "secretreason") {
		t.Fatalf("other owner's own detail carries a foreign pass: %s", rec.Body.String())
	}
	rec = serve(guests, handler, nil, httptest.NewRequest(http.MethodGet, "/api/episodes/ep-1", nil))
	if rec.Code == http.StatusOK || strings.Contains(rec.Body.String(), "secretreason") {
		t.Fatalf("anonymous status = %d body %s, want refused", rec.Code, rec.Body.String())
	}
	rec = serve(guests, handler, ownerCookie, httptest.NewRequest(http.MethodGet, "/api/episodes/ep-1", nil))
	if !strings.Contains(rec.Body.String(), `"job_id":"job-secret-edit"`) {
		t.Fatalf("owner body lacks the editorial outcome: %s", rec.Body.String())
	}
}
