package identity_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nrynss/reprise/internal/identity"
)

// callStatus asks the status route through the middleware, carrying the
// cookie when one is given.
func callStatus(t *testing.T, svc *identity.Service, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/login/status", nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	svc.Middleware(svc.StatusHandler()).ServeHTTP(rec, req)
	return rec
}

// statusBody decodes one status answer into its signed-in flag and its
// address.
func statusBody(t *testing.T, rec *httptest.ResponseRecorder) (bool, string) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status cache header = %q, want no-store", rec.Header().Get("Cache-Control"))
	}
	var body struct {
		SignedIn bool   `json:"signed_in"`
		Email    string `json:"email"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode status body: %v", err)
	}
	return body.SignedIn, body.Email
}

// TestLoginStatusAnswersSignedOutForGuest checks a guest that never
// signed in answers signed out with no address.
func TestLoginStatusAnswersSignedOutForGuest(t *testing.T) {
	t.Parallel()
	clock := &testClock{at: time.Unix(1758000000, 0)}
	svc, _, _ := openLogin(t, clock)
	cookie, _ := loginGuest(t, svc)
	signedIn, email := statusBody(t, callStatus(t, svc, cookie))
	if signedIn {
		t.Fatal("guest status answers signed in, want signed out")
	}
	if email != "" {
		t.Fatalf("guest status email = %q, want empty", email)
	}
}

// TestLoginStatusAnswersAddressForSignedInUser checks a device that
// signed in by code answers signed in with its own address.
func TestLoginStatusAnswersAddressForSignedInUser(t *testing.T) {
	t.Parallel()
	clock := &testClock{at: time.Unix(1758000000, 0)}
	svc, db, fake := openLogin(t, clock)
	cookie, _ := loginGuest(t, svc)
	requestCode(t, svc, cookie, "owner@example.com")
	signed := verifyAs(t, svc, cookie, "owner@example.com", latestCode(t, fake))
	signedIn, email := statusBody(t, callStatus(t, svc, signed))
	if !signedIn {
		t.Fatal("signed-in status answers signed out, want signed in")
	}
	if email != "owner@example.com" {
		t.Fatalf("signed-in status email = %q, want the caller own address", email)
	}
	if rowCount(t, db, "identities") != 1 {
		t.Fatal("status read changed the identity rows")
	}
}

// TestLoginStatusAnswersSignedOutAfterRemoteRevoke checks a session
// revoked elsewhere answers signed out, while the account stays for
// its other devices.
func TestLoginStatusAnswersSignedOutAfterRemoteRevoke(t *testing.T) {
	t.Parallel()
	clock := &testClock{at: time.Unix(1758000000, 0)}
	svc, db, fake := openLogin(t, clock)
	cookie, _ := loginGuest(t, svc)
	requestCode(t, svc, cookie, "owner@example.com")
	signed := verifyAs(t, svc, cookie, "owner@example.com", latestCode(t, fake))
	if err := svc.Revoke(t.Context(), sessionID(t, signed.Value)); err != nil {
		t.Fatalf("revoke session: %v", err)
	}
	signedIn, email := statusBody(t, callStatus(t, svc, signed))
	if signedIn {
		t.Fatal("revoked status answers signed in, want signed out")
	}
	if email != "" {
		t.Fatalf("revoked status email = %q, want empty", email)
	}
	if rowCount(t, db, "identities") != 1 {
		t.Fatal("revoked status removed the account identity")
	}
}
