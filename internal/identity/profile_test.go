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

// callAccount issues one account request through the middleware with its
// profile handler, carrying the cookie when one is given.
func callAccount(t *testing.T, svc *identity.Service, cookie *http.Cookie, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	reader := strings.NewReader(body)
	req := httptest.NewRequest(method, path, reader)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	svc.Middleware(svc.ProfileHandler()).ServeHTTP(rec, req)
	return rec
}

// accountBody decodes one account answer.
func accountBody(t *testing.T, rec *httptest.ResponseRecorder) (string, string) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("account status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("account cache header = %q, want no-store", rec.Header().Get("Cache-Control"))
	}
	var body struct {
		Email       string `json:"email"`
		DisplayName string `json:"display_name"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode account body: %v", err)
	}
	return body.Email, body.DisplayName
}

// signInGuest requests a code for the address and verifies it, returning
// the signed-in cookie. The helper lives beside the login tests.

// TestDisplayNameRefusesGuests requires both account routes to answer 401
// for a guest that never signed in. A guest holds no address, so there is
// no account to read or name.
func TestDisplayNameRefusesGuests(t *testing.T) {
	t.Parallel()
	clock := &testClock{at: time.Unix(1758000000, 0)}
	svc, db, _ := openLogin(t, clock)
	cookie, _ := loginGuest(t, svc)

	get := callAccount(t, svc, cookie, http.MethodGet, "/api/account", "")
	if get.Code != http.StatusUnauthorized {
		t.Fatalf("guest GET status = %d, want 401", get.Code)
	}
	put := callAccount(t, svc, cookie, http.MethodPut, "/api/account/name", `{"display_name":"Mara"}`)
	if put.Code != http.StatusUnauthorized {
		t.Fatalf("guest PUT status = %d, want 401", put.Code)
	}
	if rowCount(t, db, "users") == 0 {
		t.Fatal("guest refusal removed the user row")
	}
}

// TestDisplayNameRoundTrip signs in, stores a padded name, reads it back
// beside the address, clears it, and refuses overlong and control names.
func TestDisplayNameRoundTrip(t *testing.T) {
	t.Parallel()
	clock := &testClock{at: time.Unix(1758000000, 0)}
	svc, _, fake := openLogin(t, clock)
	cookie, _ := loginGuest(t, svc)
	requestCode(t, svc, cookie, "owner@example.com")
	signed := verifyAs(t, svc, cookie, "owner@example.com", latestCode(t, fake))

	put := func(body string) *httptest.ResponseRecorder {
		return callAccount(t, svc, signed, http.MethodPut, "/api/account/name", body)
	}
	get := func() (string, string) {
		return accountBody(t, callAccount(t, svc, signed, http.MethodGet, "/api/account", ""))
	}

	rec := put(`{"display_name":"  Mara  "}`)
	if email, name := accountBody(t, rec); email != "owner@example.com" || name != "Mara" {
		t.Fatalf("PUT answer = %q %q, want the address with Mara trimmed", email, name)
	}
	if email, name := get(); email != "owner@example.com" || name != "Mara" {
		t.Fatalf("GET answer = %q %q, want the address with Mara", email, name)
	}

	exact := strings.Repeat("n", 60)
	if _, name := accountBody(t, put(`{"display_name":`+quote(exact)+`}`)); name != exact {
		t.Fatalf("60 char name = %q, want the full value", name)
	}
	if rec := put(`{"display_name":` + quote(strings.Repeat("n", 61)) + `}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("61 char status = %d, want 400", rec.Code)
	}
	if rec := put(`{"display_name":"Ma\nra"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("control char status = %d, want 400", rec.Code)
	}
	if rec := put(`{"display_name":"Ma` + "\u0001" + `ra"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("control rune status = %d, want 400", rec.Code)
	}
	if _, name := accountBody(t, put(`{"display_name":""}`)); name != "" {
		t.Fatalf("cleared name = %q, want empty", name)
	}
	if _, name := get(); name != "" {
		t.Fatalf("GET after clear = %q, want empty", name)
	}
	if _, name := accountBody(t, put(`{"name":"Theo"}`)); name != "Theo" {
		t.Fatalf("short key name = %q, want Theo", name)
	}
}

// TestDisplayNameGoesWithUserRow stores a name, removes the user row the
// way account deletion does, and requires nothing of the name to remain.
// The name lives on the user row, so deleting the row deletes the name.
func TestDisplayNameGoesWithUserRow(t *testing.T) {
	t.Parallel()
	clock := &testClock{at: time.Unix(1758000000, 0)}
	svc, db, fake := openLogin(t, clock)
	cookie, user := loginGuest(t, svc)
	requestCode(t, svc, cookie, "owner@example.com")
	signed := verifyAs(t, svc, cookie, "owner@example.com", latestCode(t, fake))

	rec := callAccount(t, svc, signed, http.MethodPut, "/api/account/name", `{"display_name":"Mara"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var stored string
	if err := db.Reader().QueryRowContext(t.Context(),
		"SELECT display_name FROM users WHERE id = ?", user.ID).Scan(&stored); err != nil {
		t.Fatalf("read stored name: %v", err)
	}
	if stored != "Mara" {
		t.Fatalf("stored name = %q, want Mara", stored)
	}
	if _, err := db.Writer().ExecContext(t.Context(), "DELETE FROM login_codes WHERE requesting_session IN (SELECT id FROM guest_sessions WHERE user_id = ?)", user.ID); err != nil {
		t.Fatalf("delete code rows: %v", err)
	}
	if _, err := db.Writer().ExecContext(t.Context(), "DELETE FROM guest_sessions WHERE user_id = ?", user.ID); err != nil {
		t.Fatalf("delete session rows: %v", err)
	}
	if _, err := db.Writer().ExecContext(t.Context(), "DELETE FROM identities WHERE user_id = ?", user.ID); err != nil {
		t.Fatalf("delete identity rows: %v", err)
	}
	if _, err := db.Writer().ExecContext(t.Context(), "DELETE FROM users WHERE id = ?", user.ID); err != nil {
		t.Fatalf("delete user row: %v", err)
	}
	var total int
	if err := db.Reader().QueryRowContext(t.Context(),
		"SELECT COUNT(*) FROM users WHERE id = ?", user.ID).Scan(&total); err != nil {
		t.Fatalf("count users: %v", err)
	}
	if total != 0 {
		t.Fatalf("users holds %d rows, want none after deletion", total)
	}
}
