package privacy_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nrynss/reprise/internal/privacy"
)

// TestShareServesRenderAndCover pins the public page. A published
// token resolves metadata and cover bytes to a signed-out visitor,
// while an unpublished token, a revoked token, and a malformed token
// all read one identical 404.
func TestShareServesRenderAndCover(t *testing.T) {
	fx := openFixture(t)
	fx.as("owner-a")
	seed := fx.seeds["owner-a"]
	handler := fx.svc.Handler()

	unpublished := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/share/does-not-exist", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	token, err := fx.svc.Publish(t.Context(), seed.episode)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/share/"+token, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("share status %d, want 200 (%s)", rec.Code, rec.Body.String())
	}

	coverReq := httptest.NewRequest(http.MethodGet, "/api/share/"+token+"/cover", nil)
	coverRec := httptest.NewRecorder()
	handler.ServeHTTP(coverRec, coverReq)
	if coverRec.Code != http.StatusOK {
		t.Fatalf("cover status %d, want 200", coverRec.Code)
	}
	if got := coverRec.Header().Get("Content-Type"); got != "image/png" {
		t.Fatalf("cover type %q, want image/png", got)
	}
	if coverRec.Body.String() != "cover owner-a" {
		t.Fatalf("cover body %q, want the cover bytes", coverRec.Body.String())
	}

	if _, err := fx.svc.Unpublish(t.Context(), seed.episode); err != nil {
		t.Fatalf("unpublish: %v", err)
	}
	revokedBody := unpublished().Body.String()
	for name, path := range map[string]string{
		"revoked token":   "/api/share/" + token,
		"revoked cover":   "/api/share/" + token + "/cover",
		"malformed token": "/api/share/not-a-token",
		"unknown token":   "/api/share/0123456789abcdef0123456789abcdef",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s status %d, want 404", name, rec.Code)
		}
		if rec.Body.String() != revokedBody {
			t.Fatalf("%s body differs from the unknown token body", name)
		}
	}
	if _, err := fx.svc.LookupShare(t.Context(), "not-a-token"); !errors.Is(err, privacy.ErrNotFound) {
		t.Fatalf("malformed lookup err %v, want not found", err)
	}
}

// TestShareReachesNothingElse pins the negative space. The share
// handler serves metadata and cover and no other path, so there is no
// route from a token to a stem, a transcript, a thread, or another
// episode.
func TestShareReachesNothingElse(t *testing.T) {
	fx := openFixture(t)
	fx.as("owner-a")
	seed := fx.seeds["owner-a"]
	token, err := fx.svc.Publish(t.Context(), seed.episode)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	handler := fx.svc.Handler()

	for name, path := range map[string]string{
		"stem":       "/api/share/" + token + "/stems",
		"transcript": "/api/share/" + token + "/transcript",
		"threads":    "/api/share/" + token + "/threads",
		"episodes":   "/api/share/" + token + "/episodes",
		"nested":     "/api/share/" + token + "/cover/extra",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s path status %d, want 404", name, rec.Code)
		}
	}
	if got := signedOutMedia(fx, t, seed.stems[0]).Code; got != http.StatusNotFound {
		t.Fatalf("signed-out stem status %d, want 404", got)
	}
}
