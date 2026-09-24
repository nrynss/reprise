package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestProbeForeignDetailLeaksNothing is a review probe. A second owner
// asking for the first owner's detail gets 404 and no media id.
func TestProbeForeignDetailLeaksNothing(t *testing.T) {
	db, guests := openDiary(t)
	_, owner := mintGuest(t, guests)
	otherCookie, _ := mintGuest(t, guests)
	seedEpisodeRow(t, db, "ep-1", owner.ID, 1, "draft")
	seedWordRow(t, db, "w1", owner.ID, "ep-1", "secret", "edit", 0, 100)
	seedStemRow(t, db, "s1", owner.ID, "ep-1", "user-blob", "user")
	seedRenderRow(t, db, "r1", owner.ID, "ep-1", "opus-blob")
	handler := NewEpisodes(newEpisodeService(t, db, nil).svc)
	rec := serve(guests, handler, otherCookie, httptest.NewRequest(http.MethodGet, "/api/episodes/ep-1", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	body := rec.Body.String()
	for _, leak := range []string{"secret", "user-blob", "opus-blob"} {
		if strings.Contains(body, leak) {
			t.Fatalf("body leaks %q: %s", leak, body)
		}
	}
	rec = serve(guests, handler, nil, httptest.NewRequest(http.MethodGet, "/api/episodes/ep-1", nil))
	if rec.Code == http.StatusOK {
		t.Fatalf("anonymous detail answered 200")
	}
}
