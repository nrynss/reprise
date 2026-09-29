package identity_test

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nrynss/reprise/internal/identity"
)

// fakeGoogle serves the provider endpoints one Google test needs. It
// signs its own tokens with an in-test key, serves its own discovery
// document and key set, and checks the PKCE verifier against the
// challenge the start sent. No packet leaves the loopback.
type fakeGoogle struct {
	t         *testing.T
	key       *rsa.PrivateKey
	signKey   *rsa.PrivateKey
	kid       string
	server    *httptest.Server
	mu        sync.Mutex
	code      string
	challenge string
	claims    map[string]any
	verifier  string
	calls     int
}

// openFakeGoogle starts one provider with a fresh signing key.
func openFakeGoogle(t *testing.T) *fakeGoogle {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate signing key: %v", err)
	}
	fake := &fakeGoogle{t: t, key: key, kid: "test-key", code: "good-code"}
	fake.signKey = key
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", fake.serveDiscovery)
	mux.HandleFunc("/keys", fake.serveKeys)
	mux.HandleFunc("/token", fake.serveToken)
	fake.server = httptest.NewServer(mux)
	t.Cleanup(fake.server.Close)
	return fake
}

// base returns the provider origin the config cites as its issuer.
func (f *fakeGoogle) base() string {
	return f.server.URL
}

// config builds the service config pointing at this provider.
func (f *fakeGoogle) config() identity.GoogleConfig {
	return identity.GoogleConfig{
		Issuer:       f.base(),
		ClientID:     "test-client-id",
		ClientSecret: "test-client-secret",
		RedirectURL:  "http://localhost:8080/api/login/google/callback",
		HTTPClient:   f.server.Client(),
	}
}

// jwkPublic renders the test key as a set entry.
func (f *fakeGoogle) jwkPublic() map[string]string {
	pub := f.key.Public().(*rsa.PublicKey)
	return map[string]string{
		"kty": "RSA",
		"kid": f.kid,
		"use": "sig",
		"alg": "RS256",
		"n":   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
	}
}

// serveDiscovery answers the issuer document citing this server.
func (f *fakeGoogle) serveDiscovery(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"issuer":                 f.base(),
		"authorization_endpoint": f.base() + "/auth",
		"token_endpoint":         f.base() + "/token",
		"jwks_uri":               f.base() + "/keys",
	})
}

// serveKeys answers the key set with the test key.
func (f *fakeGoogle) serveKeys(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{f.jwkPublic()}})
}

// mint signs one token with the given claims. Callers hold the fake
// lock or set claims before the server runs.
func (f *fakeGoogle) mint(claims map[string]any) string {
	f.t.Helper()
	header, err := json.Marshal(map[string]string{"alg": "RS256", "kid": f.kid, "typ": "JWT"})
	if err != nil {
		f.t.Fatalf("encode token header: %v", err)
	}
	body, err := json.Marshal(claims)
	if err != nil {
		f.t.Fatalf("encode token claims: %v", err)
	}
	signed := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(body)
	digest := sha256.Sum256([]byte(signed))
	sig, err := rsa.SignPKCS1v15(rand.Reader, f.signKey, crypto.SHA256, digest[:])
	if err != nil {
		f.t.Fatalf("sign token: %v", err)
	}
	return signed + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// serveToken checks the code and the PKCE verifier, then answers one
// signed token. A mismatch refuses, which proves the exchange binds
// the start that holds the verifier.
func (f *fakeGoogle) serveToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	verifier := r.Form.Get("code_verifier")
	f.verifier = verifier
	if r.Form.Get("code") != f.code || r.Form.Get("grant_type") != "authorization_code" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"refused"}`))
		return
	}
	if verifier == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"refused"}`))
		return
	}
	if f.challenge != "" {
		sum := sha256.Sum256([]byte(verifier))
		if base64.RawURLEncoding.EncodeToString(sum[:]) != f.challenge {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"refused"}`))
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"id_token": f.mint(f.claims), "token_type": "Bearer"})
}

// setClaims stores the claims the next mint signs.
func (f *fakeGoogle) setClaims(claims map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.claims = claims
}

// standardClaims builds live claims for one subject and nonce. The
// expiry reads one hour past the test clock, so only the expired case
// overrides it.
func standardClaims(issuer, client, sub, nonce string, exp int64) map[string]any {
	return map[string]any{
		"iss": issuer, "aud": client, "sub": sub,
		"exp": exp, "iat": exp - 3600, "nonce": nonce,
	}
}

// googleClock returns the fixed clock the Google tests share.
func googleClock() *testClock {
	return &testClock{at: time.Unix(1758000000, 0)}
}

// startGoogleFlow starts one flow and returns its URL with the state,
// the nonce and the challenge the redirect carries.
func startGoogleFlow(t *testing.T, svc *identity.Service, cfg identity.GoogleConfig, cookie *http.Cookie, user identity.User) (string, string, string, string) {
	t.Helper()
	old := sessionID(t, cookie.Value)
	target, err := svc.StartGoogleLogin(t.Context(), cfg, old, user.ID)
	if err != nil {
		t.Fatalf("start google login: %v", err)
	}
	parsed, err := url.Parse(target)
	if err != nil {
		t.Fatalf("parse auth redirect: %v", err)
	}
	query := parsed.Query()
	return target, query.Get("state"), query.Get("nonce"), query.Get("code_challenge")
}

// TestGoogleStartBuildsOpenIDRedirect checks the start redirects with
// state, nonce and PKCE, and asks for the OpenID scope only.
func TestGoogleStartBuildsOpenIDRedirect(t *testing.T) {
	t.Parallel()
	clock := googleClock()
	svc, _, _ := openLogin(t, clock)
	fake := openFakeGoogle(t)
	cfg := fake.config()
	cookie, user := loginGuest(t, svc)
	target, state, nonce, challenge := startGoogleFlow(t, svc, cfg, cookie, user)
	parsed, err := url.Parse(target)
	if err != nil {
		t.Fatalf("parse auth redirect: %v", err)
	}
	if parsed.Host != strings.TrimPrefix(fake.base(), "http://") {
		t.Fatalf("auth redirect host = %q, want the test issuer", parsed.Host)
	}
	query := parsed.Query()
	if query.Get("response_type") != "code" {
		t.Fatalf("response_type = %q, want code", query.Get("response_type"))
	}
	if query.Get("scope") != "openid" {
		t.Fatalf("scope = %q, want only openid", query.Get("scope"))
	}
	if query.Get("client_id") != cfg.ClientID {
		t.Fatalf("client_id = %q, want the test client", query.Get("client_id"))
	}
	if query.Get("code_challenge_method") != "S256" {
		t.Fatalf("challenge method = %q, want S256", query.Get("code_challenge_method"))
	}
	if state == "" || nonce == "" || challenge == "" {
		t.Fatal("start left state, nonce or challenge empty")
	}
	fake.challenge = challenge
	fake.setClaims(standardClaims(cfg.Issuer, cfg.ClientID, "google-sub-1", nonce, clock.at.Unix()+3600))
	old := sessionID(t, cookie.Value)
	outcome, err := svc.CompleteGoogleLogin(t.Context(), cfg, old, user.ID, state, fake.code, "")
	if err != nil {
		t.Fatalf("complete google login: %v", err)
	}
	if outcome.UserID != user.ID {
		t.Fatal("first sign-in moved the device off its user")
	}
	fake.mu.Lock()
	seen := fake.verifier
	fake.mu.Unlock()
	sum := sha256.Sum256([]byte(seen))
	if base64.RawURLEncoding.EncodeToString(sum[:]) != challenge {
		t.Fatal("exchanged verifier does not match the start challenge")
	}
}

// TestGoogleStartRefusesWithoutConfig checks an empty client stops the
// start before any provider call.
func TestGoogleStartRefusesWithoutConfig(t *testing.T) {
	t.Parallel()
	clock := googleClock()
	svc, _, _ := openLogin(t, clock)
	cookie, user := loginGuest(t, svc)
	_, err := svc.StartGoogleLogin(t.Context(), identity.GoogleConfig{}, sessionID(t, cookie.Value), user.ID)
	if !errors.Is(err, identity.ErrGoogleNotConfigured) {
		t.Fatalf("start error = %v, want not configured", err)
	}
}

// TestGoogleCompleteRefusesBadState checks an unknown state stops the
// callback before any provider call.
func TestGoogleCompleteRefusesBadState(t *testing.T) {
	t.Parallel()
	clock := googleClock()
	svc, _, _ := openLogin(t, clock)
	fake := openFakeGoogle(t)
	cookie, user := loginGuest(t, svc)
	old := sessionID(t, cookie.Value)
	_, err := svc.CompleteGoogleLogin(t.Context(), fake.config(), old, user.ID, "unknown-state", fake.code, "")
	if !errors.Is(err, identity.ErrGoogleState) {
		t.Fatalf("complete error = %v, want bad state", err)
	}
	fake.mu.Lock()
	calls := fake.calls
	fake.mu.Unlock()
	if calls != 0 {
		t.Fatalf("token endpoint saw %d calls, want none before state passes", calls)
	}
}

// TestGoogleCompleteRefusesBadNonce checks a token echoing the wrong
// nonce never resolves.
func TestGoogleCompleteRefusesBadNonce(t *testing.T) {
	t.Parallel()
	clock := googleClock()
	svc, _, _ := openLogin(t, clock)
	fake := openFakeGoogle(t)
	cfg := fake.config()
	cookie, user := loginGuest(t, svc)
	_, state, _, challenge := startGoogleFlow(t, svc, cfg, cookie, user)
	fake.challenge = challenge
	fake.setClaims(standardClaims(cfg.Issuer, cfg.ClientID, "google-sub-1", "wrong-nonce", clock.at.Unix()+3600))
	_, err := svc.CompleteGoogleLogin(t.Context(), cfg, sessionID(t, cookie.Value), user.ID, state, fake.code, "")
	if !errors.Is(err, identity.ErrGoogleToken) {
		t.Fatalf("complete error = %v, want refused token", err)
	}
}

// TestGoogleCompleteRefusesWrongAudience checks a token for another
// client never resolves.
func TestGoogleCompleteRefusesWrongAudience(t *testing.T) {
	t.Parallel()
	clock := googleClock()
	svc, _, _ := openLogin(t, clock)
	fake := openFakeGoogle(t)
	cfg := fake.config()
	cookie, user := loginGuest(t, svc)
	_, state, nonce, challenge := startGoogleFlow(t, svc, cfg, cookie, user)
	fake.challenge = challenge
	fake.setClaims(standardClaims(cfg.Issuer, "another-client", "google-sub-1", nonce, clock.at.Unix()+3600))
	_, err := svc.CompleteGoogleLogin(t.Context(), cfg, sessionID(t, cookie.Value), user.ID, state, fake.code, "")
	if !errors.Is(err, identity.ErrGoogleToken) {
		t.Fatalf("complete error = %v, want refused token", err)
	}
}

// TestGoogleCompleteRefusesExpiredToken checks a past expiry never
// resolves, even with every other claim correct.
func TestGoogleCompleteRefusesExpiredToken(t *testing.T) {
	t.Parallel()
	clock := googleClock()
	svc, _, _ := openLogin(t, clock)
	fake := openFakeGoogle(t)
	cfg := fake.config()
	cookie, user := loginGuest(t, svc)
	_, state, nonce, challenge := startGoogleFlow(t, svc, cfg, cookie, user)
	fake.challenge = challenge
	fake.setClaims(standardClaims(cfg.Issuer, cfg.ClientID, "google-sub-1", nonce, clock.at.Unix()-60))
	_, err := svc.CompleteGoogleLogin(t.Context(), cfg, sessionID(t, cookie.Value), user.ID, state, fake.code, "")
	if !errors.Is(err, identity.ErrGoogleToken) {
		t.Fatalf("complete error = %v, want refused token", err)
	}
}

// TestGoogleCompleteRefusesWrongIssuer checks a token citing another
// issuer never resolves.
func TestGoogleCompleteRefusesWrongIssuer(t *testing.T) {
	t.Parallel()
	clock := googleClock()
	svc, _, _ := openLogin(t, clock)
	fake := openFakeGoogle(t)
	cfg := fake.config()
	cookie, user := loginGuest(t, svc)
	_, state, nonce, challenge := startGoogleFlow(t, svc, cfg, cookie, user)
	fake.challenge = challenge
	fake.setClaims(standardClaims("http://other-issuer.invalid", cfg.ClientID, "google-sub-1", nonce, clock.at.Unix()+3600))
	_, err := svc.CompleteGoogleLogin(t.Context(), cfg, sessionID(t, cookie.Value), user.ID, state, fake.code, "")
	if !errors.Is(err, identity.ErrGoogleToken) {
		t.Fatalf("complete error = %v, want refused token", err)
	}
}

// TestGoogleCompleteRefusesBadSignature checks a token signed by
// another key never resolves.
func TestGoogleCompleteRefusesBadSignature(t *testing.T) {
	t.Parallel()
	clock := googleClock()
	svc, _, _ := openLogin(t, clock)
	fake := openFakeGoogle(t)
	cfg := fake.config()
	cookie, user := loginGuest(t, svc)
	_, state, nonce, challenge := startGoogleFlow(t, svc, cfg, cookie, user)
	fake.challenge = challenge
	other, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate other key: %v", err)
	}
	fake.mu.Lock()
	fake.signKey = other
	fake.mu.Unlock()
	fake.setClaims(standardClaims(cfg.Issuer, cfg.ClientID, "google-sub-1", nonce, clock.at.Unix()+3600))
	err = nil
	_, err = svc.CompleteGoogleLogin(t.Context(), cfg, sessionID(t, cookie.Value), user.ID, state, fake.code, "")
	if !errors.Is(err, identity.ErrGoogleToken) {
		t.Fatalf("complete error = %v, want refused token", err)
	}
}

// TestGoogleCompleteRefusesBadCode checks a refused exchange never
// resolves.
func TestGoogleCompleteRefusesBadCode(t *testing.T) {
	t.Parallel()
	clock := googleClock()
	svc, _, _ := openLogin(t, clock)
	fake := openFakeGoogle(t)
	cfg := fake.config()
	cookie, user := loginGuest(t, svc)
	_, state, nonce, challenge := startGoogleFlow(t, svc, cfg, cookie, user)
	fake.challenge = challenge
	fake.setClaims(standardClaims(cfg.Issuer, cfg.ClientID, "google-sub-1", nonce, clock.at.Unix()+3600))
	_, err := svc.CompleteGoogleLogin(t.Context(), cfg, sessionID(t, cookie.Value), user.ID, state, "spent-code", "")
	if !errors.Is(err, identity.ErrGoogleToken) {
		t.Fatalf("complete error = %v, want refused token", err)
	}
}

// completeGoogle signs one flow through to success and returns the
// outcome. The fake answers the test subject with live claims.
func completeGoogle(t *testing.T, svc *identity.Service, fake *fakeGoogle, clock *testClock, cookie *http.Cookie, user identity.User, sub string) identity.LoginOutcome {
	t.Helper()
	cfg := fake.config()
	_, state, nonce, challenge := startGoogleFlow(t, svc, cfg, cookie, user)
	fake.challenge = challenge
	fake.setClaims(standardClaims(cfg.Issuer, cfg.ClientID, sub, nonce, clock.at.Unix()+3600))
	outcome, err := svc.CompleteGoogleLogin(t.Context(), cfg, sessionID(t, cookie.Value), user.ID, state, fake.code, "")
	if err != nil {
		t.Fatalf("complete google login: %v", err)
	}
	return outcome
}

// TestGoogleAttachesOwnerOnFirstUse checks the first sign-in keys the
// identity by subject, flips the user to owner, and rotates the
// session. The address never keys anything.
func TestGoogleAttachesOwnerOnFirstUse(t *testing.T) {
	t.Parallel()
	clock := googleClock()
	svc, db, _ := openLogin(t, clock)
	fake := openFakeGoogle(t)
	cookie, user := loginGuest(t, svc)
	old := sessionID(t, cookie.Value)
	outcome := completeGoogle(t, svc, fake, clock, cookie, user, "google-sub-first")
	if outcome.UserID != user.ID {
		t.Fatal("first sign-in moved the device off its user")
	}
	if outcome.Switched {
		t.Fatal("first sign-in reports a switch on its own user")
	}
	var holder, provider, subject string
	if err := db.Reader().QueryRowContext(t.Context(),
		"SELECT user_id, provider, subject FROM identities").Scan(&holder, &provider, &subject); err != nil {
		t.Fatalf("read identity: %v", err)
	}
	if holder != user.ID || provider != "google" || subject != "google-sub-first" {
		t.Fatalf("identity = %q %q %q, want the guest keyed by subject", holder, provider, subject)
	}
	var kind string
	if err := db.Reader().QueryRowContext(t.Context(),
		"SELECT kind FROM users WHERE id = ?", user.ID).Scan(&kind); err != nil {
		t.Fatalf("read user kind: %v", err)
	}
	if kind != identity.KindOwner {
		t.Fatalf("user kind = %q, want owner", kind)
	}
	if outcome.SessionID == old {
		t.Fatal("session still carries the old session after success")
	}
	if !sessionRevoked(t, db, old) {
		t.Fatal("old session row is not revoked after success")
	}
	if sessionUser(t, db, outcome.SessionID) != user.ID {
		t.Fatal("fresh session points at a different user than the guest")
	}
}

// TestGoogleKeepsIdentityOnSameUser checks a second sign-in on the
// same user changes nothing but the session.
func TestGoogleKeepsIdentityOnSameUser(t *testing.T) {
	t.Parallel()
	clock := googleClock()
	svc, db, _ := openLogin(t, clock)
	fake := openFakeGoogle(t)
	cookie, user := loginGuest(t, svc)
	first := completeGoogle(t, svc, fake, clock, cookie, user, "google-sub-steady")
	signed := httptest.NewRecorder()
	svc.SetSessionCookie(signed, first.SessionID)
	fresh := signed.Result().Cookies()[0]
	second := completeGoogle(t, svc, fake, clock, fresh, user, "google-sub-steady")
	if second.UserID != user.ID {
		t.Fatal("second sign-in moved the device off its user")
	}
	if rowCount(t, db, "identities") != 1 {
		t.Fatalf("identities holds %d rows, want the one attach", rowCount(t, db, "identities"))
	}
	if sessionUser(t, db, second.SessionID) != user.ID {
		t.Fatal("second sign-in moved the device off its user")
	}
}

// TestGoogleSwitchesEmptyGuestToAccount checks a guest with no
// episodes moves to the account user on first complete.
func TestGoogleSwitchesEmptyGuestToAccount(t *testing.T) {
	t.Parallel()
	clock := googleClock()
	svc, db, _ := openLogin(t, clock)
	fake := openFakeGoogle(t)
	ownerCookie, owner := loginGuest(t, svc)
	completeGoogle(t, svc, fake, clock, ownerCookie, owner, "google-sub-roamer")
	guestCookie, guest := loginGuest(t, svc)
	old := sessionID(t, guestCookie.Value)
	outcome := completeGoogle(t, svc, fake, clock, guestCookie, guest, "google-sub-roamer")
	if !outcome.Switched {
		t.Fatal("switch to the account reports no switch")
	}
	if sessionUser(t, db, outcome.SessionID) != owner.ID {
		t.Fatal("switched session points at the guest instead of the account")
	}
	if !sessionRevoked(t, db, old) {
		t.Fatal("guest session row is not revoked after the switch")
	}
	if rowCount(t, db, "identities") != 1 {
		t.Fatalf("identities holds %d rows, want the one attach", rowCount(t, db, "identities"))
	}
}

// TestGoogleConflictsOnGuestDiaryThenSwitches checks a guest with
// episodes gets the conflict with no change, and the switch choice
// moves it on the same state without a code.
func TestGoogleConflictsOnGuestDiaryThenSwitches(t *testing.T) {
	t.Parallel()
	clock := googleClock()
	svc, db, _ := openLogin(t, clock)
	fake := openFakeGoogle(t)
	cfg := fake.config()
	ownerCookie, owner := loginGuest(t, svc)
	completeGoogle(t, svc, fake, clock, ownerCookie, owner, "google-sub-settled")
	guestCookie, guest := loginGuest(t, svc)
	seedEpisode(t, db, "ep-diary", guest.ID)
	old := sessionID(t, guestCookie.Value)
	_, state, nonce, challenge := startGoogleFlow(t, svc, cfg, guestCookie, guest)
	fake.challenge = challenge
	fake.setClaims(standardClaims(cfg.Issuer, cfg.ClientID, "google-sub-settled", nonce, clock.at.Unix()+3600))
	_, err := svc.CompleteGoogleLogin(t.Context(), cfg, old, guest.ID, state, fake.code, "")
	if !errors.Is(err, identity.ErrDiaryConflict) {
		t.Fatalf("conflict complete error = %v, want diary conflict", err)
	}
	if sessionRevoked(t, db, old) {
		t.Fatal("conflict revoked the guest session, want no change")
	}
	if rowCount(t, db, "identities") != 1 {
		t.Fatalf("identities holds %d rows, want the one attach", rowCount(t, db, "identities"))
	}
	outcome, err := svc.CompleteGoogleLogin(t.Context(), cfg, old, guest.ID, state, "", "switch")
	if err != nil {
		t.Fatalf("switch complete: %v", err)
	}
	if sessionUser(t, db, outcome.SessionID) != owner.ID {
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

// TestGoogleHandlerServesStartAndCallback checks the HTTP pair the
// boot mounts: the start redirects to the issuer, and the callback
// rotates the session and lands on the done flag.
func TestGoogleHandlerServesStartAndCallback(t *testing.T) {
	t.Parallel()
	clock := googleClock()
	svc, db, _ := openLogin(t, clock)
	fake := openFakeGoogle(t)
	cfg := fake.config()
	cookie, user := loginGuest(t, svc)
	handler := svc.Middleware(svc.GoogleHandler(cfg))
	startReq := httptest.NewRequest(http.MethodGet, "/api/login/google/start", nil)
	startReq.AddCookie(cookie)
	startRec := httptest.NewRecorder()
	handler.ServeHTTP(startRec, startReq)
	if startRec.Code != http.StatusSeeOther {
		t.Fatalf("start status = %d, want 303: %s", startRec.Code, startRec.Body.String())
	}
	target := startRec.Header().Get("Location")
	if !strings.HasPrefix(target, fake.base()+"/auth?") {
		t.Fatalf("start redirect = %q, want the test issuer", target)
	}
	parsed, err := url.Parse(target)
	if err != nil {
		t.Fatalf("parse auth redirect: %v", err)
	}
	query := parsed.Query()
	state, nonce, challenge := query.Get("state"), query.Get("nonce"), query.Get("code_challenge")
	fake.challenge = challenge
	fake.setClaims(standardClaims(cfg.Issuer, cfg.ClientID, "google-sub-page", nonce, clock.at.Unix()+3600))
	old := sessionID(t, cookie.Value)
	backReq := httptest.NewRequest(http.MethodGet,
		"/api/login/google/callback?code="+fake.code+"&state="+url.QueryEscape(state), nil)
	backReq.AddCookie(cookie)
	backRec := httptest.NewRecorder()
	handler.ServeHTTP(backRec, backReq)
	if backRec.Code != http.StatusSeeOther {
		t.Fatalf("callback status = %d, want 303: %s", backRec.Code, backRec.Body.String())
	}
	if backRec.Header().Get("Location") != "/account/google/callback?done=1" {
		t.Fatalf("callback redirect = %q, want the done flag", backRec.Header().Get("Location"))
	}
	var next *http.Cookie
	for _, c := range backRec.Result().Cookies() {
		if c.Name == identity.CookieName {
			next = c
		}
	}
	if next == nil {
		t.Fatal("callback set no session cookie")
	}
	if sessionUser(t, db, sessionID(t, next.Value)) != user.ID {
		t.Fatal("callback session points at a different user than the guest")
	}
	if !sessionRevoked(t, db, old) {
		t.Fatal("callback left the old session live")
	}
}

// TestGoogleHandlerFlagsConflict checks the callback lands on the
// conflict flag with the state, and the switch retry on the same
// state moves the device without a code.
func TestGoogleHandlerFlagsConflict(t *testing.T) {
	t.Parallel()
	clock := googleClock()
	svc, db, _ := openLogin(t, clock)
	fake := openFakeGoogle(t)
	cfg := fake.config()
	ownerCookie, owner := loginGuest(t, svc)
	completeGoogle(t, svc, fake, clock, ownerCookie, owner, "google-sub-clash")
	guestCookie, guest := loginGuest(t, svc)
	seedEpisode(t, db, "ep-clash", guest.ID)
	handler := svc.Middleware(svc.GoogleHandler(cfg))
	startReq := httptest.NewRequest(http.MethodGet, "/api/login/google/start", nil)
	startReq.AddCookie(guestCookie)
	startRec := httptest.NewRecorder()
	handler.ServeHTTP(startRec, startReq)
	if startRec.Code != http.StatusSeeOther {
		t.Fatalf("start status = %d, want 303", startRec.Code)
	}
	parsed, err := url.Parse(startRec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse auth redirect: %v", err)
	}
	query := parsed.Query()
	state, nonce, challenge := query.Get("state"), query.Get("nonce"), query.Get("code_challenge")
	fake.challenge = challenge
	fake.setClaims(standardClaims(cfg.Issuer, cfg.ClientID, "google-sub-clash", nonce, clock.at.Unix()+3600))
	backReq := httptest.NewRequest(http.MethodGet,
		"/api/login/google/callback?code="+fake.code+"&state="+url.QueryEscape(state), nil)
	backReq.AddCookie(guestCookie)
	backRec := httptest.NewRecorder()
	handler.ServeHTTP(backRec, backReq)
	if backRec.Code != http.StatusSeeOther {
		t.Fatalf("callback status = %d, want 303: %s", backRec.Code, backRec.Body.String())
	}
	want := "/account/google/callback?conflict=1&state=" + url.QueryEscape(state)
	if backRec.Header().Get("Location") != want {
		t.Fatalf("callback redirect = %q, want %q", backRec.Header().Get("Location"), want)
	}
	switchReq := httptest.NewRequest(http.MethodGet,
		"/api/login/google/callback?state="+url.QueryEscape(state)+"&choice=switch", nil)
	switchReq.AddCookie(guestCookie)
	switchRec := httptest.NewRecorder()
	handler.ServeHTTP(switchRec, switchReq)
	if switchRec.Code != http.StatusSeeOther {
		t.Fatalf("switch status = %d, want 303: %s", switchRec.Code, switchRec.Body.String())
	}
	if switchRec.Header().Get("Location") != "/account/google/callback?done=1" {
		t.Fatalf("switch redirect = %q, want the done flag", switchRec.Header().Get("Location"))
	}
	var next *http.Cookie
	for _, c := range switchRec.Result().Cookies() {
		if c.Name == identity.CookieName {
			next = c
		}
	}
	if next == nil {
		t.Fatal("switch set no session cookie")
	}
	if sessionUser(t, db, sessionID(t, next.Value)) != owner.ID {
		t.Fatal("switch left the device on the guest")
	}
}
