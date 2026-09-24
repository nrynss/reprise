package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestProbeForeignRenderWordsLeakNothing is a review probe. A second
// owner asking for the first owner's detail gets 404 and none of the
// edit words, rendered words, stem id or render id. The owner's own
// detail names each blob only through the media route.
func TestProbeForeignRenderWordsLeakNothing(t *testing.T) {
	db, guests := openDiary(t)
	ownerCookie, owner := mintGuest(t, guests)
	otherCookie, other := mintGuest(t, guests)
	seedEpisodeRow(t, db, "ep-1", owner.ID, 1, "ready")
	seedWordRow(t, db, "w1", owner.ID, "ep-1", "secretedit", "edit", 0, 100)
	seedWordRow(t, db, "w2", owner.ID, "ep-1", "secretrendered", "rendered", 0, 100)
	seedStemRow(t, db, "s1", owner.ID, "ep-1", "user-blob", "user")
	seedRenderRow(t, db, "r1", owner.ID, "ep-1", "opus-blob")
	// The other owner holds an episode of their own, so a filter that
	// dropped the owner would still find rows to return.
	seedEpisodeRow(t, db, "ep-2", other.ID, 1, "ready")
	handler := NewEpisodes(newEpisodeService(t, db, nil).svc)

	rec := serve(guests, handler, otherCookie, httptest.NewRequest(http.MethodGet, "/api/episodes/ep-1", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("foreign status = %d, want 404", rec.Code)
	}
	for _, leak := range []string{"secretedit", "secretrendered", "user-blob", "opus-blob"} {
		if strings.Contains(rec.Body.String(), leak) {
			t.Fatalf("foreign body leaks %q: %s", leak, rec.Body.String())
		}
	}
	rec = serve(guests, handler, otherCookie, httptest.NewRequest(http.MethodGet, "/api/episodes/ep-2", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("own status = %d, want 200", rec.Code)
	}
	for _, leak := range []string{"secretedit", "secretrendered", "user-blob", "opus-blob"} {
		if strings.Contains(rec.Body.String(), leak) {
			t.Fatalf("other owner's own detail leaks %q: %s", leak, rec.Body.String())
		}
	}
	rec = serve(guests, handler, ownerCookie, httptest.NewRequest(http.MethodGet, "/api/episodes/ep-1", nil))
	body := rec.Body.String()
	if !strings.Contains(body, `"audio_url":"/media/user-blob"`) || !strings.Contains(body, `"render_audio_url":"/media/opus-blob"`) {
		t.Fatalf("owner body names a blob outside the media route: %s", body)
	}
	if !strings.Contains(body, "secretrendered") {
		t.Fatalf("owner body lacks the rendered words: %s", body)
	}
}
