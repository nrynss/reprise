package identity_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nrynss/reprise/internal/identity"
)

// loginMux serves every login route behind one handler, the way the boot
// mounts them: the sign-in pair beside the sign-out route.
func loginMux(svc *identity.Service) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/api/login/", svc.LoginHandler())
	mux.Handle("POST /api/login/signout", svc.SignOutHandler())
	return mux
}

// callSignout posts to the sign-out route through the middleware,
// carrying the cookie when one is given.
func callSignout(t *testing.T, svc *identity.Service, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/login/signout", strings.NewReader(`{}`))
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	svc.Middleware(loginMux(svc)).ServeHTTP(rec, req)
	return rec
}

// verifyAs signs one guest in as one address and returns the signed
// cookie the verify response set.
func verifyAs(t *testing.T, svc *identity.Service, cookie *http.Cookie, email string, code string) *http.Cookie {
	t.Helper()
	rec := callLogin(t, svc, cookie, "/api/login/verify",
		`{"email":`+quote(email)+`,"code":`+quote(code)+`}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("verify status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	return responseCookie(t, rec)
}

// TestSignOutRevokesSessionAndMintsFreshGuest checks a signed-in device
// that signs out loses its session and gains a fresh guest. The old
// cookie resolves as a stranger, and the stored identity stays for the
// other devices.
func TestSignOutRevokesSessionAndMintsFreshGuest(t *testing.T) {
	t.Parallel()
	clock := &testClock{at: time.Unix(1758000000, 0)}
	svc, db, fake := openLogin(t, clock)
	cookie, user := loginGuest(t, svc)
	oldSession := sessionID(t, cookie.Value)
	requestCode(t, svc, cookie, "leaver@example.com")
	signed := verifyAs(t, svc, cookie, "leaver@example.com", latestCode(t, fake))
	signedSession := sessionID(t, signed.Value)
	rec := callSignout(t, svc, signed)
	if rec.Code != http.StatusOK {
		t.Fatalf("sign-out status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || !body.OK {
		t.Fatalf("sign-out body = %q, want the shared ok shape", rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("sign-out cache header = %q, want no-store", rec.Header().Get("Cache-Control"))
	}
	next := responseCookie(t, rec)
	if sessionID(t, next.Value) == signedSession {
		t.Fatal("sign-out cookie still carries the revoked session")
	}
	if !sessionRevoked(t, db, signedSession) {
		t.Fatal("signed-in session row is not revoked after sign-out")
	}
	if !sessionRevoked(t, db, oldSession) {
		t.Fatal("old session row is not revoked after sign-out")
	}
	freshUser := sessionUser(t, db, sessionID(t, next.Value))
	if freshUser == user.ID {
		t.Fatal("fresh session still points at the signed-in user")
	}
	var kind string
	if err := db.Reader().QueryRowContext(t.Context(),
		"SELECT kind FROM users WHERE id = ?", freshUser).Scan(&kind); err != nil {
		t.Fatalf("read fresh user kind: %v", err)
	}
	if kind != identity.KindGuest {
		t.Fatalf("fresh user kind = %q, want guest", kind)
	}
	if rowCount(t, db, "identities") != 1 {
		t.Fatalf("identities holds %d rows, want the untouched account", rowCount(t, db, "identities"))
	}
	again, got, _ := visit(svc, signed, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	if again.Code != http.StatusOK {
		t.Fatalf("old cookie visit status = %d, want a fresh mint at 200", again.Code)
	}
	if got.ID == user.ID {
		t.Fatal("revoked cookie still resolves to the signed-in user")
	}
}

// TestSignOutAnswersSameBodyForGuestAndOwner checks a guest that never
// signed in gets the byte identical 200 body a signed-in device gets,
// with a fresh guest cookie each time.
func TestSignOutAnswersSameBodyForGuestAndOwner(t *testing.T) {
	t.Parallel()
	clock := &testClock{at: time.Unix(1758000000, 0)}
	svc, _, fake := openLogin(t, clock)
	cookie, _ := loginGuest(t, svc)
	requestCode(t, svc, cookie, "owner@example.com")
	signed := verifyAs(t, svc, cookie, "owner@example.com", latestCode(t, fake))
	ownerRec := callSignout(t, svc, signed)

	guestCookie, _ := loginGuest(t, svc)
	guestRec := callSignout(t, svc, guestCookie)
	if guestRec.Code != http.StatusOK {
		t.Fatalf("guest sign-out status = %d, want 200: %s", guestRec.Code, guestRec.Body.String())
	}
	if ownerRec.Body.String() != guestRec.Body.String() {
		t.Fatalf("sign-out bodies differ:\nowner %q\nguest %q", ownerRec.Body.String(), guestRec.Body.String())
	}
	ownerNext := responseCookie(t, ownerRec)
	guestNext := responseCookie(t, guestRec)
	if sessionID(t, ownerNext.Value) == sessionID(t, signed.Value) {
		t.Fatal("owner sign-out cookie still carries the revoked session")
	}
	if sessionID(t, guestNext.Value) == sessionID(t, guestCookie.Value) {
		t.Fatal("guest sign-out cookie still carries the revoked session")
	}
}
