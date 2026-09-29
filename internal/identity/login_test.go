package identity_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/nrynss/keel/sqlite"
	"github.com/nrynss/reprise/internal/identity"
	"github.com/nrynss/reprise/internal/mail"
	"github.com/nrynss/reprise/internal/store"
)

// loginCodeKey is the HMAC key the login tests wire. It never leaves the
// test process.
const loginCodeKey = "test-login-code-key-with-length"

// openLogin wires a service with a mail fake and a fixed clock, on a
// database carrying the diary tables the resolution reads.
func openLogin(t *testing.T, clock *testClock) (*identity.Service, *sqlite.DB, *mail.Fake) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "diary.db")
	db, err := sqlite.Open(t.Context(), sqlite.Config{
		Path:   path,
		Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := store.Open(t.Context(), db); err != nil {
		t.Fatalf("open diary store: %v", err)
	}
	fake := &mail.Fake{}
	svc, err := identity.New(t.Context(), identity.Config{
		DB:           db,
		SigningKey:   "test-signing-key-with-enough-length",
		LoginCodeKey: loginCodeKey,
		Mail:         fake,
		Now:          clock.now,
	})
	if err != nil {
		t.Fatalf("open identity service: %v", err)
	}
	return svc, db, fake
}

// loginGuest mints one guest through the middleware and returns its
// cookie with its user.
func loginGuest(t *testing.T, svc *identity.Service) (*http.Cookie, identity.User) {
	t.Helper()
	quiet := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	rec, user, ok := visit(svc, nil, quiet)
	if !ok {
		t.Fatal("middleware set no user in context")
	}
	return sessionCookie(t, rec), user
}

// callLogin posts one body to a login route through the middleware,
// carrying the cookie when one is given.
func callLogin(t *testing.T, svc *identity.Service, cookie *http.Cookie, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	svc.Middleware(svc.LoginHandler()).ServeHTTP(rec, req)
	return rec
}

// requestCode asks for a code and fails the test on any answer but 202.
func requestCode(t *testing.T, svc *identity.Service, cookie *http.Cookie, email string) *httptest.ResponseRecorder {
	t.Helper()
	rec := callLogin(t, svc, cookie, "/api/login/code", `{"email":`+quote(email)+`}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("code request status = %d, want 202: %s", rec.Code, rec.Body.String())
	}
	return rec
}

// quote renders one JSON string without importing the encoder in tests.
func quote(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

// latestCode pulls the six digit code from the most recent fake mail.
func latestCode(t *testing.T, fake *mail.Fake) string {
	t.Helper()
	msgs := fake.Messages()
	if len(msgs) == 0 {
		t.Fatal("mail fake recorded no message")
	}
	found := regexp.MustCompile(`\b\d{6}\b`).FindString(msgs[len(msgs)-1].Text)
	if found == "" {
		t.Fatalf("last mail carries no six digit code: %q", msgs[len(msgs)-1].Text)
	}
	return found
}

// responseCookie returns the session cookie a login response set.
func responseCookie(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == identity.CookieName {
			return c
		}
	}
	t.Fatalf("response set no %q cookie", identity.CookieName)
	return nil
}

// sessionUser reads the user one session row points at.
func sessionUser(t *testing.T, db *sqlite.DB, sessionID string) string {
	t.Helper()
	var userID string
	if err := db.Reader().QueryRowContext(t.Context(),
		"SELECT user_id FROM guest_sessions WHERE id = ?", sessionID).Scan(&userID); err != nil {
		t.Fatalf("read session row: %v", err)
	}
	return userID
}

// sessionRevoked reports whether one session row is revoked.
func sessionRevoked(t *testing.T, db *sqlite.DB, sessionID string) bool {
	t.Helper()
	var revoked int
	if err := db.Reader().QueryRowContext(t.Context(),
		"SELECT revoked FROM guest_sessions WHERE id = ?", sessionID).Scan(&revoked); err != nil {
		t.Fatalf("read session row: %v", err)
	}
	return revoked != 0
}

// hmacHex authenticates one value with the test login key.
func hmacHex(value string) string {
	mac := hmac.New(sha256.New, []byte(loginCodeKey))
	_, _ = mac.Write([]byte(value))
	return hex.EncodeToString(mac.Sum(nil))
}

// TestLoginCodeAnswersSameBodyForKnownAndUnknown checks the code route
// answers byte identical bodies for an address with an account and one
// without, and mails both.
func TestLoginCodeAnswersSameBodyForKnownAndUnknown(t *testing.T) {
	t.Parallel()
	clock := &testClock{at: time.Unix(1758000000, 0)}
	svc, db, fake := openLogin(t, clock)
	stranger, _ := loginGuest(t, svc)
	unknown := requestCode(t, svc, stranger, "new-guest@example.com")

	owner, _ := loginGuest(t, svc)
	requestCode(t, svc, owner, "owner-known@example.com")
	ownerCode := latestCode(t, fake)
	ownerVerify := callLogin(t, svc, owner, "/api/login/verify",
		`{"email":"owner-known@example.com","code":`+quote(ownerCode)+`}`)
	if ownerVerify.Code != http.StatusOK {
		t.Fatalf("owner seed verify status = %d, want 200", ownerVerify.Code)
	}

	other, _ := loginGuest(t, svc)
	known := requestCode(t, svc, other, "  OWNER-known@Example.COM ")
	if unknown.Body.String() != known.Body.String() {
		t.Fatalf("known body %q differs from unknown body %q", known.Body.String(), unknown.Body.String())
	}
	msgs := fake.Messages()
	if len(msgs) != 3 {
		t.Fatalf("mail fake holds %d messages, want one per request", len(msgs))
	}
	if msgs[2].To != "owner-known@example.com" {
		t.Fatalf("mail went to %q, want the normalized address", msgs[2].To)
	}
	var subject string
	if err := db.Reader().QueryRowContext(t.Context(),
		"SELECT subject FROM identities WHERE provider = 'email'").Scan(&subject); err != nil {
		t.Fatalf("read identity: %v", err)
	}
	if subject != "owner-known@example.com" {
		t.Fatalf("identity subject = %q, want the normalized address", subject)
	}
}

// TestLoginCodeMailCarriesExactTextWithoutLink checks the mail body
// matches the product wording exactly and carries no link.
func TestLoginCodeMailCarriesExactTextWithoutLink(t *testing.T) {
	t.Parallel()
	clock := &testClock{at: time.Unix(1758000000, 0)}
	svc, _, fake := openLogin(t, clock)
	cookie, _ := loginGuest(t, svc)
	requestCode(t, svc, cookie, "reader@example.com")
	msgs := fake.Messages()
	if len(msgs) != 1 {
		t.Fatalf("mail fake holds %d messages, want one", len(msgs))
	}
	if msgs[0].Subject == "" {
		t.Fatal("mail carries no subject")
	}
	want := "Your Reprise sign-in code is " + latestCode(t, fake) + ". It expires in 10 minutes. If you did not ask for it, ignore this mail."
	if msgs[0].Text != want {
		t.Fatalf("mail text = %q, want %q", msgs[0].Text, want)
	}
	if strings.Contains(msgs[0].Text, "http") {
		t.Fatalf("mail text carries a link: %q", msgs[0].Text)
	}
}

// TestLoginCodeStoresHashesNeverPlaintext checks the stored row holds
// HMAC hashes that match the test key, and no column holds the code.
func TestLoginCodeStoresHashesNeverPlaintext(t *testing.T) {
	t.Parallel()
	clock := &testClock{at: time.Unix(1758000000, 0)}
	svc, db, fake := openLogin(t, clock)
	cookie, _ := loginGuest(t, svc)
	requestCode(t, svc, cookie, "quiet@example.com")
	code := latestCode(t, fake)
	var id, addressHash, address, codeHash, requesting string
	var expires, attempts, used, created int64
	if err := db.Reader().QueryRowContext(t.Context(), `SELECT id, address_hash, address,
		code_hash, requesting_session, expires_at, attempts, used_at, created_at FROM login_codes`).Scan(
		&id, &addressHash, &address, &codeHash, &requesting, &expires, &attempts, &used, &created); err != nil {
		t.Fatalf("read code row: %v", err)
	}
	if codeHash != hmacHex(code) {
		t.Fatal("stored code hash matches no HMAC of the code with the login key")
	}
	if addressHash != hmacHex("quiet@example.com") {
		t.Fatal("stored address hash matches no HMAC of the address with the login key")
	}
	dump := fmt.Sprintf("%v|%v|%v|%v|%v|%v|%v|%v|%v",
		id, addressHash, address, codeHash, requesting, expires, attempts, used, created)
	if strings.Contains(dump, code) {
		t.Fatalf("stored row holds the plain code: %q", dump)
	}
	if expires != clock.at.Unix()+600 {
		t.Fatalf("code expiry = %d, want ten minutes past the request", expires)
	}
}

// TestLoginCodeRefusesEmptyAddress checks a missing address answers 400
// and sends nothing.
func TestLoginCodeRefusesEmptyAddress(t *testing.T) {
	t.Parallel()
	clock := &testClock{at: time.Unix(1758000000, 0)}
	svc, _, fake := openLogin(t, clock)
	cookie, _ := loginGuest(t, svc)
	for _, body := range []string{`{}`, `{"email":""}`, `{"email":"   "}`, `{oops`} {
		rec := callLogin(t, svc, cookie, "/api/login/code", body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body %q: status = %d, want 400", body, rec.Code)
		}
	}
	if len(fake.Messages()) != 0 {
		t.Fatalf("mail fake holds %d messages, want none", len(fake.Messages()))
	}
}

// TestLoginRequestRetiresEarlierCode checks a second request kills the
// first code and the second still verifies.
func TestLoginRequestRetiresEarlierCode(t *testing.T) {
	t.Parallel()
	clock := &testClock{at: time.Unix(1758000000, 0)}
	svc, _, fake := openLogin(t, clock)
	cookie, _ := loginGuest(t, svc)
	requestCode(t, svc, cookie, "repeat@example.com")
	first := latestCode(t, fake)
	requestCode(t, svc, cookie, "repeat@example.com")
	second := latestCode(t, fake)
	stale := callLogin(t, svc, cookie, "/api/login/verify",
		`{"email":"repeat@example.com","code":`+quote(first)+`}`)
	if stale.Code != http.StatusUnauthorized {
		t.Fatalf("retired code status = %d, want 401", stale.Code)
	}
	fresh := callLogin(t, svc, cookie, "/api/login/verify",
		`{"email":"repeat@example.com","code":`+quote(second)+`}`)
	if fresh.Code != http.StatusOK {
		t.Fatalf("fresh code status = %d, want 200", fresh.Code)
	}
}

// TestLoginVerifyAttachesOwnerOnFirstUse checks the first verify for an
// address attaches the identity, flips the kind, and rotates the session.
func TestLoginVerifyAttachesOwnerOnFirstUse(t *testing.T) {
	t.Parallel()
	clock := &testClock{at: time.Unix(1758000000, 0)}
	svc, db, fake := openLogin(t, clock)
	cookie, user := loginGuest(t, svc)
	oldSession := sessionID(t, cookie.Value)
	requestCode(t, svc, cookie, "first@example.com")
	rec := callLogin(t, svc, cookie, "/api/login/verify",
		`{"email":"first@example.com","code":`+quote(latestCode(t, fake))+`}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("verify status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var holder, provider, subject string
	if err := db.Reader().QueryRowContext(t.Context(),
		"SELECT user_id, provider, subject FROM identities").Scan(&holder, &provider, &subject); err != nil {
		t.Fatalf("read identity: %v", err)
	}
	if holder != user.ID || provider != "email" || subject != "first@example.com" {
		t.Fatalf("identity = %q %q %q, want the guest with this address", holder, provider, subject)
	}
	var kind string
	if err := db.Reader().QueryRowContext(t.Context(),
		"SELECT kind FROM users WHERE id = ?", user.ID).Scan(&kind); err != nil {
		t.Fatalf("read user kind: %v", err)
	}
	if kind != identity.KindOwner {
		t.Fatalf("user kind = %q, want owner", kind)
	}
	next := responseCookie(t, rec)
	if sessionID(t, next.Value) == oldSession {
		t.Fatal("session cookie still carries the old session after success")
	}
	if !sessionRevoked(t, db, oldSession) {
		t.Fatal("old session row is not revoked after success")
	}
	if sessionUser(t, db, sessionID(t, next.Value)) != user.ID {
		t.Fatal("fresh session points at a different user than the guest")
	}
	again, got, _ := visit(svc, cookie, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	if again.Code != http.StatusOK {
		t.Fatalf("old cookie visit status = %d, want a fresh mint at 200", again.Code)
	}
	if got.ID == user.ID {
		t.Fatal("revoked cookie still resolves to the signed-in user")
	}
}

// TestLoginVerifyKeepsIdentityOnSameUser checks a second sign-in on the
// same user changes nothing but the session.
func TestLoginVerifyKeepsIdentityOnSameUser(t *testing.T) {
	t.Parallel()
	clock := &testClock{at: time.Unix(1758000000, 0)}
	svc, db, fake := openLogin(t, clock)
	cookie, user := loginGuest(t, svc)
	requestCode(t, svc, cookie, "steady@example.com")
	first := callLogin(t, svc, cookie, "/api/login/verify",
		`{"email":"steady@example.com","code":`+quote(latestCode(t, fake))+`}`)
	if first.Code != http.StatusOK {
		t.Fatalf("first verify status = %d, want 200", first.Code)
	}
	next := responseCookie(t, first)
	requestCode(t, svc, next, "steady@example.com")
	second := callLogin(t, svc, next, "/api/login/verify",
		`{"email":"steady@example.com","code":`+quote(latestCode(t, fake))+`}`)
	if second.Code != http.StatusOK {
		t.Fatalf("second verify status = %d, want 200", second.Code)
	}
	if rowCount(t, db, "identities") != 1 {
		t.Fatalf("identities holds %d rows, want the one attach", rowCount(t, db, "identities"))
	}
	if sessionUser(t, db, sessionID(t, responseCookie(t, second).Value)) != user.ID {
		t.Fatal("second sign-in moved the device off its user")
	}
}

// TestLoginVerifySwitchesEmptyGuestToAccount checks a guest with no
// episodes moves to the account user on first verify.
func TestLoginVerifySwitchesEmptyGuestToAccount(t *testing.T) {
	t.Parallel()
	clock := &testClock{at: time.Unix(1758000000, 0)}
	svc, db, fake := openLogin(t, clock)
	ownerCookie, owner := loginGuest(t, svc)
	requestCode(t, svc, ownerCookie, "roamer@example.com")
	seed := callLogin(t, svc, ownerCookie, "/api/login/verify",
		`{"email":"roamer@example.com","code":`+quote(latestCode(t, fake))+`}`)
	if seed.Code != http.StatusOK {
		t.Fatalf("account seed verify status = %d, want 200", seed.Code)
	}

	guestCookie, guest := loginGuest(t, svc)
	oldSession := sessionID(t, guestCookie.Value)
	requestCode(t, svc, guestCookie, "roamer@example.com")
	rec := callLogin(t, svc, guestCookie, "/api/login/verify",
		`{"email":"roamer@example.com","code":`+quote(latestCode(t, fake))+`}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("switch verify status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	moved := sessionID(t, responseCookie(t, rec).Value)
	if sessionUser(t, db, moved) != owner.ID {
		t.Fatal("switched session points at the guest instead of the account")
	}
	if !sessionRevoked(t, db, oldSession) {
		t.Fatal("guest session row is not revoked after the switch")
	}
	if rowCount(t, db, "identities") != 1 {
		t.Fatalf("identities holds %d rows, want the one attach", rowCount(t, db, "identities"))
	}
	var guests int
	if err := db.Reader().QueryRowContext(t.Context(),
		"SELECT COUNT(*) FROM users WHERE id = ?", guest.ID).Scan(&guests); err != nil {
		t.Fatalf("read guest row: %v", err)
	}
	if guests != 1 {
		t.Fatal("empty guest user row vanished on switch, want retention to own it")
	}
}

// TestLoginVerifyConflictsOnGuestDiaryThenSwitches checks a guest with
// episodes gets 409 with no change, and the switch choice moves it.
func TestLoginVerifyConflictsOnGuestDiaryThenSwitches(t *testing.T) {
	t.Parallel()
	clock := &testClock{at: time.Unix(1758000000, 0)}
	svc, db, fake := openLogin(t, clock)
	ownerCookie, owner := loginGuest(t, svc)
	requestCode(t, svc, ownerCookie, "settled@example.com")
	if rec := callLogin(t, svc, ownerCookie, "/api/login/verify",
		`{"email":"settled@example.com","code":`+quote(latestCode(t, fake))+`}`); rec.Code != http.StatusOK {
		t.Fatalf("account seed verify status = %d, want 200", rec.Code)
	}

	guestCookie, guest := loginGuest(t, svc)
	seedEpisode(t, db, "ep-diary", guest.ID)
	oldSession := sessionID(t, guestCookie.Value)
	requestCode(t, svc, guestCookie, "settled@example.com")
	code := latestCode(t, fake)
	conflict := callLogin(t, svc, guestCookie, "/api/login/verify",
		`{"email":"settled@example.com","code":`+quote(code)+`}`)
	if conflict.Code != http.StatusConflict {
		t.Fatalf("conflict verify status = %d, want 409", conflict.Code)
	}
	if !strings.Contains(conflict.Body.String(), identity.CodeDiaryConflict) {
		t.Fatalf("conflict body %q carries no diary conflict code", conflict.Body.String())
	}
	if sessionRevoked(t, db, oldSession) {
		t.Fatal("conflict revoked the guest session, want no change")
	}
	var used int
	if err := db.Reader().QueryRowContext(t.Context(),
		"SELECT used_at FROM login_codes WHERE address_hash = ? AND requesting_session = ?",
		hmacHex("settled@example.com"), oldSession).Scan(&used); err != nil {
		t.Fatalf("read code row: %v", err)
	}
	if used != 0 {
		t.Fatal("conflict consumed the code, want it live for the switch choice")
	}
	move := callLogin(t, svc, guestCookie, "/api/login/verify",
		`{"email":"settled@example.com","code":`+quote(code)+`,"choice":"switch"}`)
	if move.Code != http.StatusOK {
		t.Fatalf("switch verify status = %d, want 200: %s", move.Code, move.Body.String())
	}
	if sessionUser(t, db, sessionID(t, responseCookie(t, move).Value)) != owner.ID {
		t.Fatal("switch choice left the device on the guest")
	}
	var keeper string
	if err := db.Reader().QueryRowContext(t.Context(),
		"SELECT owner_id FROM episodes WHERE id = 'ep-diary'").Scan(&keeper); err != nil {
		t.Fatalf("read guest episode: %v", err)
	}
	if keeper != guest.ID {
		t.Fatal("switch moved the guest episode, want it left to retention")
	}
}

// TestLoginVerifyRefusesForeignSessionCode checks a code requested by one
// session never verifies on another, and stays live for its owner.
func TestLoginVerifyRefusesForeignSessionCode(t *testing.T) {
	t.Parallel()
	clock := &testClock{at: time.Unix(1758000000, 0)}
	svc, _, fake := openLogin(t, clock)
	cookieA, _ := loginGuest(t, svc)
	cookieB, _ := loginGuest(t, svc)
	requestCode(t, svc, cookieA, "shared@example.com")
	code := latestCode(t, fake)
	foreign := callLogin(t, svc, cookieB, "/api/login/verify",
		`{"email":"shared@example.com","code":`+quote(code)+`}`)
	if foreign.Code != http.StatusUnauthorized {
		t.Fatalf("foreign session status = %d, want 401", foreign.Code)
	}
	home := callLogin(t, svc, cookieA, "/api/login/verify",
		`{"email":"shared@example.com","code":`+quote(code)+`}`)
	if home.Code != http.StatusOK {
		t.Fatalf("requesting session status = %d, want 200", home.Code)
	}
}

// TestLoginVerifyDiesAfterSixthWrongTry checks five wrong tries still
// leave a code usable, while the sixth closes it for good.
func TestLoginVerifyDiesAfterSixthWrongTry(t *testing.T) {
	t.Parallel()
	clock := &testClock{at: time.Unix(1758000000, 0)}
	svc, db, fake := openLogin(t, clock)
	cookie, _ := loginGuest(t, svc)
	requestCode(t, svc, cookie, "patient@example.com")
	first := latestCode(t, fake)
	for i := 0; i < 5; i++ {
		rec := callLogin(t, svc, cookie, "/api/login/verify",
			`{"email":"patient@example.com","code":"000000"}`)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("wrong try %d status = %d, want 401", i+1, rec.Code)
		}
	}
	if rec := callLogin(t, svc, cookie, "/api/login/verify",
		`{"email":"patient@example.com","code":`+quote(first)+`}`); rec.Code != http.StatusOK {
		t.Fatalf("correct code after five wrong tries status = %d, want 200", rec.Code)
	}

	cookie2, _ := loginGuest(t, svc)
	requestCode(t, svc, cookie2, "locked@example.com")
	second := latestCode(t, fake)
	for i := 0; i < 6; i++ {
		rec := callLogin(t, svc, cookie2, "/api/login/verify",
			`{"email":"locked@example.com","code":"000000"}`)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("wrong try %d status = %d, want 401", i+1, rec.Code)
		}
	}
	if rec := callLogin(t, svc, cookie2, "/api/login/verify",
		`{"email":"locked@example.com","code":`+quote(second)+`}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("correct code after six wrong tries status = %d, want 401", rec.Code)
	}
	var used int
	var address string
	if err := db.Reader().QueryRowContext(t.Context(),
		"SELECT used_at, address FROM login_codes WHERE address_hash = ?",
		hmacHex("locked@example.com")).Scan(&used, &address); err != nil {
		t.Fatalf("read closed code: %v", err)
	}
	if used == 0 {
		t.Fatal("code past six wrong tries is still live")
	}
	if address != "" {
		t.Fatalf("closed code still stores address %q", address)
	}
}

// TestLoginVerifyRefusesExpiredCode checks a code past ten minutes dies
// and clears its plain address.
func TestLoginVerifyRefusesExpiredCode(t *testing.T) {
	t.Parallel()
	start := time.Unix(1758000000, 0)
	clock := &testClock{at: start}
	svc, db, fake := openLogin(t, clock)
	cookie, _ := loginGuest(t, svc)
	requestCode(t, svc, cookie, "late@example.com")
	code := latestCode(t, fake)
	clock.at = start.Add(11 * time.Minute)
	rec := callLogin(t, svc, cookie, "/api/login/verify",
		`{"email":"late@example.com","code":`+quote(code)+`}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expired code status = %d, want 401", rec.Code)
	}
	var address string
	if err := db.Reader().QueryRowContext(t.Context(),
		"SELECT address FROM login_codes WHERE address_hash = ?",
		hmacHex("late@example.com")).Scan(&address); err != nil {
		t.Fatalf("read expired code: %v", err)
	}
	if address != "" {
		t.Fatalf("expired code still stores address %q", address)
	}
}

// TestLoginVerifyWorksOnce checks a consumed code never verifies again.
func TestLoginVerifyWorksOnce(t *testing.T) {
	t.Parallel()
	clock := &testClock{at: time.Unix(1758000000, 0)}
	svc, _, fake := openLogin(t, clock)
	cookie, _ := loginGuest(t, svc)
	requestCode(t, svc, cookie, "single@example.com")
	code := latestCode(t, fake)
	first := callLogin(t, svc, cookie, "/api/login/verify",
		`{"email":"single@example.com","code":`+quote(code)+`}`)
	if first.Code != http.StatusOK {
		t.Fatalf("first verify status = %d, want 200", first.Code)
	}
	next := responseCookie(t, first)
	second := callLogin(t, svc, next, "/api/login/verify",
		`{"email":"single@example.com","code":`+quote(code)+`}`)
	if second.Code != http.StatusUnauthorized {
		t.Fatalf("reused code status = %d, want 401", second.Code)
	}
}
