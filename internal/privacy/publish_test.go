package privacy_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nrynss/keel/id"
	"github.com/nrynss/reprise/internal/privacy"
)

// signedOutMedia serves one blob id through the real media store with
// no session, the way a workstation fetch arrives.
func signedOutMedia(fx *fixture, t *testing.T, blob string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/media/"+blob, nil)
	req.SetPathValue("id", blob)
	rec := httptest.NewRecorder()
	fx.media.ServeHTTP(rec, req)
	return rec
}

// TestPublishOpensRenderOnly pins the publish half. The streaming
// render serves to a signed-out visitor with an immutable cache. The
// stems, the export copy, and the share metadata behind a revoked
// token all refuse.
func TestPublishOpensRenderOnly(t *testing.T) {
	fx := openFixture(t)
	fx.as("owner-a")
	seed := fx.seeds["owner-a"]

	token, err := fx.svc.Publish(t.Context(), seed.episode)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if !id.Valid(token) {
		t.Fatalf("publish token %q has the wrong shape", token)
	}

	var visibility string
	if err := fx.db.Reader().QueryRowContext(t.Context(),
		"SELECT visibility FROM episodes WHERE id = ?", seed.episode).Scan(&visibility); err != nil {
		t.Fatalf("read visibility: %v", err)
	}
	if visibility != privacy.VisibilityPublic {
		t.Fatalf("episode visibility %q, want public", visibility)
	}
	if got := fx.blobVisible(t, seed.opus); got != "public" {
		t.Fatalf("opus visibility %q, want public", got)
	}

	rec := signedOutMedia(fx, t, seed.opus)
	if rec.Code != http.StatusOK {
		t.Fatalf("signed-out render status %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Fatalf("render cache %q, want the immutable public cache", got)
	}
	if rec.Body.String() != "render opus owner-a" {
		t.Fatalf("render body %q, want the opus bytes", rec.Body.String())
	}

	for name, blob := range map[string]string{"user stem": seed.stems[0], "host stem": seed.stems[1], "export": seed.aac} {
		if got := signedOutMedia(fx, t, blob).Code; got != http.StatusNotFound {
			t.Fatalf("signed-out %s status %d, want 404", name, got)
		}
	}

	share, err := fx.svc.LookupShare(t.Context(), token)
	if err != nil {
		t.Fatalf("lookup share: %v", err)
	}
	if share.AudioID != seed.opus {
		t.Fatalf("share audio %q, want the opus blob", share.AudioID)
	}
	if share.Title == "" || share.Number != 1 {
		t.Fatalf("share metadata %+v, want title and number", share)
	}

	again, err := fx.svc.Publish(t.Context(), seed.episode)
	if err != nil {
		t.Fatalf("republish: %v", err)
	}
	if again == token {
		t.Fatalf("republish kept the token, want a fresh one")
	}
	if _, err := fx.svc.LookupShare(t.Context(), token); !errors.Is(err, privacy.ErrNotFound) {
		t.Fatalf("old token resolves, want not found (err %v)", err)
	}
}

// TestUnpublishRevokesToken pins the refuse half. The rotated link
// dies at once, the render returns to private, and the old token reads
// exactly like a token that never existed.
func TestUnpublishRevokesToken(t *testing.T) {
	fx := openFixture(t)
	fx.as("owner-a")
	seed := fx.seeds["owner-a"]

	token, err := fx.svc.Publish(t.Context(), seed.episode)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if got := signedOutMedia(fx, t, seed.opus).Code; got != http.StatusOK {
		t.Fatalf("signed-out render status %d, want 200", got)
	}

	rotated, err := fx.svc.Unpublish(t.Context(), seed.episode)
	if err != nil {
		t.Fatalf("unpublish: %v", err)
	}
	if rotated == token {
		t.Fatalf("unpublish kept the token, want a rotated one")
	}
	if _, err := fx.svc.LookupShare(t.Context(), token); !errors.Is(err, privacy.ErrNotFound) {
		t.Fatalf("old token resolves, want not found (err %v)", err)
	}
	if _, err := fx.svc.LookupShare(t.Context(), rotated); !errors.Is(err, privacy.ErrNotFound) {
		t.Fatalf("rotated token resolves while private, want not found (err %v)", err)
	}
	if got := signedOutMedia(fx, t, seed.opus).Code; got != http.StatusNotFound {
		t.Fatalf("signed-out render status %d, want 404", got)
	}
	if got := fx.blobVisible(t, seed.opus); got != "private" {
		t.Fatalf("opus visibility %q, want private", got)
	}
}

// TestPublishRefusals pins the boundaries. Another owner's episode,
// a missing episode, and an episode with no render all refuse with
// their own sentinel.
func TestPublishRefusals(t *testing.T) {
	fx := openFixture(t)
	other := fx.seeds["owner-b"]

	fx.as("owner-a")
	if _, err := fx.svc.Publish(t.Context(), other.episode); !errors.Is(err, privacy.ErrNotOwner) {
		t.Fatalf("foreign publish err %v, want not owner", err)
	}
	if _, err := fx.svc.Unpublish(t.Context(), other.episode); !errors.Is(err, privacy.ErrNotOwner) {
		t.Fatalf("foreign unpublish err %v, want not owner", err)
	}
	if _, err := fx.svc.Erase(t.Context(), other.episode); !errors.Is(err, privacy.ErrNotOwner) {
		t.Fatalf("foreign erase err %v, want not owner", err)
	}
	if _, err := fx.svc.Publish(t.Context(), "no-such-episode"); !errors.Is(err, privacy.ErrNotFound) {
		t.Fatalf("missing publish err %v, want not found", err)
	}

	fx.exec(t, `INSERT INTO episodes
		(id, owner_id, number, title, state, visibility, share_token, seeded)
		VALUES ('bare-ep', 'owner-a', 9, 'Bare', 'draft', 'private', 'bare-token', 0)`)
	if _, err := fx.svc.Publish(t.Context(), "bare-ep"); !errors.Is(err, privacy.ErrNoRender) {
		t.Fatalf("renderless publish err %v, want no render", err)
	}
}

// TestPublishEndpoints pins the HTTP layer. The owner mints and
// revokes over the handler, a signed-out caller reads 404, and a
// wrong method reads 405.
func TestPublishEndpoints(t *testing.T) {
	fx := openFixture(t)
	fx.as("owner-a")
	seed := fx.seeds["owner-a"]
	handler := fx.svc.Handler()

	call := func(method, path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	rec := call(http.MethodPost, "/api/episodes/"+seed.episode+"/publish")
	if rec.Code != http.StatusOK {
		t.Fatalf("publish status %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if got := signedOutMedia(fx, t, seed.opus).Code; got != http.StatusOK {
		t.Fatalf("signed-out render status %d, want 200", got)
	}

	fx.as("nobody")
	if got := call(http.MethodPost, "/api/episodes/"+seed.episode+"/publish").Code; got != http.StatusNotFound {
		t.Fatalf("signed-out publish status %d, want 404", got)
	}
	if got := call(http.MethodDelete, "/api/episodes/"+seed.episode).Code; got != http.StatusNotFound {
		t.Fatalf("signed-out erase status %d, want 404", got)
	}
	if got := call(http.MethodGet, "/api/episodes/"+seed.episode+"/publish").Code; got != http.StatusMethodNotAllowed {
		t.Fatalf("publish GET status %d, want 405", got)
	}

	fx.as("owner-a")
	if got := call(http.MethodDelete, "/api/episodes/"+seed.episode+"/publish").Code; got != http.StatusOK {
		t.Fatalf("unpublish status %d, want 200", got)
	}
	if got := signedOutMedia(fx, t, seed.opus).Code; got != http.StatusNotFound {
		t.Fatalf("signed-out render status %d, want 404", got)
	}
}
