package identity

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/nrynss/keel/id"
	"github.com/nrynss/keel/wire"
)

// Google sign-in runs the authorization code flow with state, nonce and
// PKCE. The start builds the provider redirect and keeps the nonce and
// the verifier beside the session that asked. The callback exchanges the
// code, checks the token, and resolves the same way the code flow does.
// The identity key is the provider subject, never the address.

// googleProvider names the provider subject pair this flow stores. One
// user may hold several identities, and one identity maps to one user.
const googleProvider = "google"

// Google sign-in bounds. A start stays valid for ten minutes, the same
// life a mailed code carries. Bodies stay capped, so a large POST never
// reaches the decoder.
const (
	googlePendingLife = 10 * time.Minute
	googleMaxBody     = 1 << 20
)

// googleCallbackPage is the static page the callback redirects to. It
// renders the outcome flags the redirect carries, and its conflict
// choice links back to the account screen for keeping.
const googleCallbackPage = "/account/google/callback"

// ErrGoogleNotConfigured reports a Google route the process cannot
// serve. The client id, the secret, the issuer or the redirect URL is
// empty, so the route refuses until the boot wires all four.
var ErrGoogleNotConfigured = errors.New("identity: google sign-in is not configured")

// ErrGoogleState reports a callback with no usable start. The state is
// unknown, expired, or bound to another session, so the callback stops
// before any provider call.
var ErrGoogleState = errors.New("identity: google sign-in state is unknown or expired")

// ErrGoogleToken reports a provider token the server refuses. The code
// exchange failed, or the token carries the wrong issuer, audience,
// expiry, nonce, signature or subject. One code covers every case, so
// the answer never teaches which guard tripped.
var ErrGoogleToken = errors.New("identity: google token was refused")

// GoogleConfig carries what the Google flow needs beyond the service.
// Issuer is the provider origin, such as the public Google origin. The
// client pair comes from the provider console, and RedirectURL is the
// callback the console registered. HTTPClient calls the provider, and
// nil means a client with a thirty second ceiling.
type GoogleConfig struct {
	// Issuer is the provider origin the tokens must cite.
	Issuer string
	// ClientID is the public OAuth client the audience must cite.
	ClientID string
	// ClientSecret authenticates the code exchange.
	ClientSecret string
	// RedirectURL is the registered callback URL.
	RedirectURL string
	// HTTPClient calls the provider endpoints.
	HTTPClient *http.Client
}

// valid reports whether the flow can run. Every field must be set,
// because a half wired flow would redirect nowhere useful.
func (cfg GoogleConfig) valid() error {
	if cfg.Issuer == "" || cfg.ClientID == "" || cfg.ClientSecret == "" || cfg.RedirectURL == "" {
		return ErrGoogleNotConfigured
	}
	return nil
}

// client returns the provider caller. Tests pass their own, and the
// process default bounds every call well under the request ceiling.
func (cfg GoogleConfig) client() *http.Client {
	if cfg.HTTPClient != nil {
		return cfg.HTTPClient
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// googlePending binds one authorization start to its callback. The
// nonce and the verifier stay server side, so the browser carries only
// the state. Subject fills in after the first validation, which keeps
// the entry alive for the switch retry past a conflict.
type googlePending struct {
	// nonce is the value the token must echo.
	nonce string
	// verifier is the PKCE secret the exchange must prove.
	verifier string
	// sessionID is the guest session that asked.
	sessionID string
	// userID is the local user that asked.
	userID string
	// subject is the validated provider subject, set on conflict.
	subject string
	// validated reports the token already passed once.
	validated bool
	// expires bounds the entry in Unix seconds.
	expires int64
}

// googlePendings holds every live start. One container serves every
// sign-in, states are 128 bit random and single use on success, and
// every access sweeps expired rows first.
var googlePendings = struct {
	sync.Mutex
	rows map[string]googlePending
}{rows: make(map[string]googlePending)}

// googleRandom draws n random bytes as unpadded base64url. State,
// nonce and verifier all read this form.
func googleRandom(n int) (string, error) {
	raw := make([]byte, n)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("identity: mint google secret: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// googleChallenge derives the PKCE challenge from its verifier with
// SHA-256. The provider checks the exchange against this value.
func googleChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// googleRemember stores one start and sweeps expired rows first, so
// abandoned starts never accumulate past their life.
func googleRemember(state string, pending googlePending, now int64) {
	googlePendings.Lock()
	defer googlePendings.Unlock()
	for key, row := range googlePendings.rows {
		if row.expires <= now {
			delete(googlePendings.rows, key)
		}
	}
	googlePendings.rows[state] = pending
}

// googleLookup returns the live start for one state. An unknown or
// expired state reports false, and an expired row leaves the map.
func googleLookup(state string, now int64) (googlePending, bool) {
	googlePendings.Lock()
	defer googlePendings.Unlock()
	pending, ok := googlePendings.rows[state]
	if !ok {
		return googlePending{}, false
	}
	if pending.expires <= now {
		delete(googlePendings.rows, state)
		return googlePending{}, false
	}
	return pending, true
}

// googleForget drops one start after its success. A replayed callback
// then meets an unknown state instead of a second sign-in.
func googleForget(state string) {
	googlePendings.Lock()
	defer googlePendings.Unlock()
	delete(googlePendings.rows, state)
}

// googleRevisit keeps one start after a conflict, carrying the
// validated subject. The switch retry reads it back without a code,
// because the provider code is single use and already spent.
func googleRevisit(state string, pending googlePending) {
	googlePendings.Lock()
	defer googlePendings.Unlock()
	if _, ok := googlePendings.rows[state]; ok {
		googlePendings.rows[state] = pending
	}
}

// googleDiscovery carries the provider endpoints. The flow reads them
// from the issuer on every start, so no address is baked into code.
type googleDiscovery struct {
	// Issuer must echo the configured issuer exactly.
	Issuer string `json:"issuer"`
	// AuthURL starts the authorization redirect.
	AuthURL string `json:"authorization_endpoint"`
	// TokenURL exchanges the code.
	TokenURL string `json:"token_endpoint"`
	// KeysURL serves the signing keys.
	KeysURL string `json:"jwks_uri"`
}

// googleTokenJSON carries the code exchange answer. Only the token
// matters, and its absence refuses the flow.
type googleTokenJSON struct {
	// IDToken is the signed token the flow validates.
	IDToken string `json:"id_token"`
}

// googleHeader carries the token header. Only RSA with SHA-256 passes,
// so key confusion through another algorithm stops here.
type googleHeader struct {
	// Alg must read RS256.
	Alg string `json:"alg"`
	// Kid selects the signing key.
	Kid string `json:"kid"`
}

// googleKey carries one signing key. The flow builds the RSA key from
// the modulus and the exponent on every validation.
type googleKey struct {
	// KTY must read RSA.
	KTY string `json:"kty"`
	// KID selects this key from its set.
	KID string `json:"kid"`
	// N is the base64url modulus.
	N string `json:"n"`
	// E is the base64url exponent.
	E string `json:"e"`
}

// googleKeySet carries the provider signing keys.
type googleKeySet struct {
	// Keys holds the active signing keys.
	Keys []googleKey `json:"keys"`
}

// googleAudience reads the token audience as one string or many. The
// provider sends one, and the shape accepts both either way.
type googleAudience []string

// UnmarshalJSON reads one audience string or a list of them.
func (a *googleAudience) UnmarshalJSON(raw []byte) error {
	var one string
	if err := json.Unmarshal(raw, &one); err == nil {
		*a = googleAudience{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(raw, &many); err != nil {
		return err
	}
	*a = googleAudience(many)
	return nil
}

// googleClaims carries the validated token fields. The address never
// appears here, because the subject alone keys the identity.
type googleClaims struct {
	// Issuer must echo the configured issuer.
	Issuer string `json:"iss"`
	// Audience must cite the client id.
	Audience googleAudience `json:"aud"`
	// Subject keys the identity.
	Subject string `json:"sub"`
	// Expiry bounds the token in Unix seconds.
	Expiry int64 `json:"exp"`
	// Nonce must echo the pending nonce.
	Nonce string `json:"nonce"`
}

// fetchGoogleJSON reads one provider document up to the cap. Any
// transport fault, refusal, or malformed body maps to the token
// sentinel, so no provider wording ever reaches the caller.
func fetchGoogleJSON(ctx context.Context, client *http.Client, location string, shape any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, location, nil)
	if err != nil {
		return fmt.Errorf("%w: fetch provider document: %v", ErrGoogleToken, err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: fetch provider document: %v", ErrGoogleToken, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: provider document refused", ErrGoogleToken)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, googleMaxBody+1))
	if err != nil {
		return fmt.Errorf("%w: read provider document: %v", ErrGoogleToken, err)
	}
	if len(raw) > googleMaxBody {
		return fmt.Errorf("%w: provider document is too large", ErrGoogleToken)
	}
	if err := json.Unmarshal(raw, shape); err != nil {
		return fmt.Errorf("%w: decode provider document: %v", ErrGoogleToken, err)
	}
	return nil
}

// fetchGoogleDiscovery reads the issuer document and checks it cites
// the configured issuer. A document for another issuer stops here,
// which pins the flow to the provider the boot named.
func fetchGoogleDiscovery(ctx context.Context, client *http.Client, issuer string) (googleDiscovery, error) {
	var doc googleDiscovery
	location := strings.TrimRight(issuer, "/") + "/.well-known/openid-configuration"
	if err := fetchGoogleJSON(ctx, client, location, &doc); err != nil {
		return googleDiscovery{}, err
	}
	if doc.Issuer != issuer || doc.AuthURL == "" || doc.TokenURL == "" || doc.KeysURL == "" {
		return googleDiscovery{}, fmt.Errorf("%w: provider discovery is incomplete", ErrGoogleToken)
	}
	return doc, nil
}

// StartGoogleLogin starts one Google sign-in for a session and returns
// the provider redirect. It stores the nonce and the PKCE verifier
// beside the session, so the callback checks them without trusting the
// browser. Only the OpenID scope travels, because identity is the one
// capability the product asks for.
func (s *Service) StartGoogleLogin(ctx context.Context, cfg GoogleConfig, sessionID, userID string) (string, error) {
	if err := cfg.valid(); err != nil {
		return "", err
	}
	if sessionID == "" || userID == "" {
		return "", fmt.Errorf("%w: session and user are required", ErrInvalid)
	}
	doc, err := fetchGoogleDiscovery(ctx, cfg.client(), cfg.Issuer)
	if err != nil {
		return "", err
	}
	state, err := googleRandom(16)
	if err != nil {
		return "", err
	}
	nonce, err := googleRandom(16)
	if err != nil {
		return "", err
	}
	verifier, err := googleRandom(32)
	if err != nil {
		return "", err
	}
	query := url.Values{}
	query.Set("client_id", cfg.ClientID)
	query.Set("redirect_uri", cfg.RedirectURL)
	query.Set("response_type", "code")
	query.Set("scope", "openid")
	query.Set("state", state)
	query.Set("nonce", nonce)
	query.Set("code_challenge", googleChallenge(verifier))
	query.Set("code_challenge_method", "S256")
	googleRemember(state, googlePending{
		nonce:     nonce,
		verifier:  verifier,
		sessionID: sessionID,
		userID:    userID,
		expires:   s.now().Unix() + int64(googlePendingLife.Seconds()),
	}, s.now().Unix())
	return doc.AuthURL + "?" + query.Encode(), nil
}

// exchangeGoogleCode trades one code for its token and validates the
// answer. The verifier proves the exchange belongs to the start that
// holds it, and the token checks pin issuer, audience, expiry, nonce,
// signature and subject before the subject may resolve.
func (s *Service) exchangeGoogleCode(ctx context.Context, cfg GoogleConfig, pending googlePending, code, tokenURL string) (string, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("client_id", cfg.ClientID)
	form.Set("client_secret", cfg.ClientSecret)
	form.Set("redirect_uri", cfg.RedirectURL)
	form.Set("code_verifier", pending.verifier)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("%w: exchange code: %v", ErrGoogleToken, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := cfg.client().Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: exchange code: %v", ErrGoogleToken, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%w: token endpoint refused", ErrGoogleToken)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, googleMaxBody+1))
	if err != nil {
		return "", fmt.Errorf("%w: read token answer: %v", ErrGoogleToken, err)
	}
	if len(raw) > googleMaxBody {
		return "", fmt.Errorf("%w: token answer is too large", ErrGoogleToken)
	}
	var answer googleTokenJSON
	if err := json.Unmarshal(raw, &answer); err != nil {
		return "", fmt.Errorf("%w: decode token answer: %v", ErrGoogleToken, err)
	}
	if answer.IDToken == "" {
		return "", fmt.Errorf("%w: token answer carries no token", ErrGoogleToken)
	}
	return s.checkGoogleToken(ctx, cfg, pending.nonce, answer.IDToken)
}

// googleKeyFor returns the signing key one token header names. A
// rotation between the fetch and the check refetches once, so a fresh
// key never fails a valid token.
func googleKeyFor(ctx context.Context, client *http.Client, keysURL, kid string) (googleKey, error) {
	var set googleKeySet
	if err := fetchGoogleJSON(ctx, client, keysURL, &set); err != nil {
		return googleKey{}, err
	}
	for _, key := range set.Keys {
		if key.KID == kid {
			return key, nil
		}
	}
	set = googleKeySet{}
	if err := fetchGoogleJSON(ctx, client, keysURL, &set); err != nil {
		return googleKey{}, err
	}
	for _, key := range set.Keys {
		if key.KID == kid {
			return key, nil
		}
	}
	return googleKey{}, fmt.Errorf("%w: signing key is unknown", ErrGoogleToken)
}

// googlePublicKey builds one RSA key from its set entry. Anything but
// RSA stops here, which keeps elliptic or octet keys out of the check.
func googlePublicKey(key googleKey) (*rsa.PublicKey, error) {
	if key.KTY != "RSA" || key.N == "" || key.E == "" {
		return nil, fmt.Errorf("%w: signing key is not RSA", ErrGoogleToken)
	}
	modulus, err := base64.RawURLEncoding.DecodeString(key.N)
	if err != nil {
		return nil, fmt.Errorf("%w: decode signing key: %v", ErrGoogleToken, err)
	}
	exponent, err := base64.RawURLEncoding.DecodeString(key.E)
	if err != nil {
		return nil, fmt.Errorf("%w: decode signing key: %v", ErrGoogleToken, err)
	}
	exp := new(big.Int).SetBytes(exponent)
	if !exp.IsInt64() || exp.Int64() <= 0 || exp.Int64() > 1<<31-1 {
		return nil, fmt.Errorf("%w: signing key exponent is invalid", ErrGoogleToken)
	}
	return &rsa.PublicKey{N: new(big.Int).SetBytes(modulus), E: int(exp.Int64())}, nil
}

// checkGoogleToken validates one token and returns its subject. It
// checks the issuer, the audience, the expiry, the nonce, the RSA
// signature and the subject, in that order, and every refusal shares
// the one token sentinel.
func (s *Service) checkGoogleToken(ctx context.Context, cfg GoogleConfig, nonce, token string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("%w: token shape is invalid", ErrGoogleToken)
	}
	headerRaw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", fmt.Errorf("%w: decode token header: %v", ErrGoogleToken, err)
	}
	var header googleHeader
	if err := json.Unmarshal(headerRaw, &header); err != nil {
		return "", fmt.Errorf("%w: decode token header: %v", ErrGoogleToken, err)
	}
	if header.Alg != "RS256" || header.Kid == "" {
		return "", fmt.Errorf("%w: token algorithm is not RS256", ErrGoogleToken)
	}
	doc, err := fetchGoogleDiscovery(ctx, cfg.client(), cfg.Issuer)
	if err != nil {
		return "", err
	}
	key, err := googleKeyFor(ctx, cfg.client(), doc.KeysURL, header.Kid)
	if err != nil {
		return "", err
	}
	pub, err := googlePublicKey(key)
	if err != nil {
		return "", err
	}
	signed := parts[0] + "." + parts[1]
	digest := sha256.Sum256([]byte(signed))
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return "", fmt.Errorf("%w: decode token signature: %v", ErrGoogleToken, err)
	}
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], sig); err != nil {
		return "", fmt.Errorf("%w: token signature is invalid: %v", ErrGoogleToken, err)
	}
	payloadRaw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("%w: decode token claims: %v", ErrGoogleToken, err)
	}
	var claims googleClaims
	if err := json.Unmarshal(payloadRaw, &claims); err != nil {
		return "", fmt.Errorf("%w: decode token claims: %v", ErrGoogleToken, err)
	}
	if claims.Issuer != cfg.Issuer {
		return "", fmt.Errorf("%w: token issuer is wrong", ErrGoogleToken)
	}
	heard := false
	for _, aud := range claims.Audience {
		if aud == cfg.ClientID {
			heard = true
			break
		}
	}
	if !heard {
		return "", fmt.Errorf("%w: token audience is wrong", ErrGoogleToken)
	}
	if claims.Expiry <= s.now().Unix() {
		return "", fmt.Errorf("%w: token is expired", ErrGoogleToken)
	}
	if claims.Nonce == "" || subtle.ConstantTimeCompare([]byte(claims.Nonce), []byte(nonce)) != 1 {
		return "", fmt.Errorf("%w: token nonce is wrong", ErrGoogleToken)
	}
	if claims.Subject == "" {
		return "", fmt.Errorf("%w: token carries no subject", ErrGoogleToken)
	}
	return claims.Subject, nil
}

// CompleteGoogleLogin finishes one Google sign-in. It checks the state
// against the start, exchanges the code once, and resolves the
// identity the same way the code flow does: attach on first use, keep
// on the same user, switch an empty guest, and conflict on a guest
// diary without the switch choice. A conflict keeps the start, so the
// retry carries the state with the switch choice and no code.
func (s *Service) CompleteGoogleLogin(ctx context.Context, cfg GoogleConfig, sessionID, userID, state, code, choice string) (LoginOutcome, error) {
	if err := cfg.valid(); err != nil {
		return LoginOutcome{}, err
	}
	if choice != "" && choice != loginChoiceSwitch {
		return LoginOutcome{}, fmt.Errorf("%w: unknown choice", ErrInvalid)
	}
	pending, ok := googleLookup(state, s.now().Unix())
	if !ok {
		return LoginOutcome{}, ErrGoogleState
	}
	if pending.sessionID != sessionID || pending.userID != userID || sessionID == "" || userID == "" {
		return LoginOutcome{}, ErrGoogleState
	}
	if !pending.validated {
		if strings.TrimSpace(code) == "" {
			return LoginOutcome{}, ErrGoogleState
		}
		doc, err := fetchGoogleDiscovery(ctx, cfg.client(), cfg.Issuer)
		if err != nil {
			return LoginOutcome{}, err
		}
		subject, err := s.exchangeGoogleCode(ctx, cfg, pending, code, doc.TokenURL)
		if err != nil {
			return LoginOutcome{}, err
		}
		pending.validated = true
		pending.subject = subject
	}
	outcome, err := s.resolveProviderLogin(ctx, sessionID, userID, googleProvider, pending.subject, choice)
	if err != nil {
		if errors.Is(err, ErrDiaryConflict) {
			googleRevisit(state, pending)
			return LoginOutcome{}, ErrDiaryConflict
		}
		return LoginOutcome{}, err
	}
	googleForget(state)
	return outcome, nil
}

// resolveProviderLogin attaches or resumes one provider identity and
// rotates the session. A missing identity attaches to the current user
// and flips its kind to owner. An identity on this user changes
// nothing. An identity on another user switches an empty guest and
// conflicts on a guest diary without the switch choice. A racing
// attach heals inside the transaction exactly like the code flow, by
// joining the holder the race revealed. This seam serves every
// provider, and the code flow should call it too.
func (s *Service) resolveProviderLogin(ctx context.Context, sessionID, userID, provider, subject, choice string) (LoginOutcome, error) {
	var holder string
	err := s.db.Reader().QueryRowContext(ctx,
		"SELECT user_id FROM identities WHERE provider = ? AND subject = ?",
		provider, subject).Scan(&holder)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return LoginOutcome{}, fmt.Errorf("identity: read identity: %w", err)
	}
	if errors.Is(err, sql.ErrNoRows) {
		holder = ""
	}
	var episodes int
	if holder != "" && holder != userID {
		if err := s.db.Reader().QueryRowContext(ctx,
			"SELECT COUNT(*) FROM episodes WHERE owner_id = ?", userID).Scan(&episodes); err != nil {
			return LoginOutcome{}, fmt.Errorf("identity: count guest diary: %w", err)
		}
		if episodes > 0 && choice != loginChoiceSwitch {
			return LoginOutcome{}, ErrDiaryConflict
		}
	}
	now := s.now().Unix()
	tx, err := s.db.Writer().BeginTx(ctx, nil)
	if err != nil {
		return LoginOutcome{}, fmt.Errorf("identity: resolve login: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	target := userID
	switched := false
	switch {
	case holder == "":
		identityID, err := id.New()
		if err != nil {
			return LoginOutcome{}, fmt.Errorf("identity: resolve login: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO identities
			(id, user_id, provider, subject, created_at) VALUES (?, ?, ?, ?, ?)`,
			identityID, userID, provider, subject, now); err != nil {
			// Another device attached this subject first, so the
			// insert above collided on the provider subject pair.
			// Join that holder instead of failing, and honour the
			// diary conflict exactly as a later sign-in would.
			var fresh string
			if rerr := tx.QueryRowContext(ctx,
				"SELECT user_id FROM identities WHERE provider = ? AND subject = ?",
				provider, subject).Scan(&fresh); rerr != nil {
				return LoginOutcome{}, fmt.Errorf("identity: resolve login: %w", err)
			}
			if fresh != userID {
				if cerr := tx.QueryRowContext(ctx,
					"SELECT COUNT(*) FROM episodes WHERE owner_id = ?", userID).Scan(&episodes); cerr != nil {
					return LoginOutcome{}, fmt.Errorf("identity: resolve login: %w", cerr)
				}
				if episodes > 0 && choice != loginChoiceSwitch {
					return LoginOutcome{}, ErrDiaryConflict
				}
				target = fresh
				switched = true
			}
		} else if _, err := tx.ExecContext(ctx,
			"UPDATE users SET kind = ? WHERE id = ?", KindOwner, userID); err != nil {
			return LoginOutcome{}, fmt.Errorf("identity: resolve login: %w", err)
		}
	case holder != userID:
		target = holder
		switched = true
	}
	nextSession, err := id.New()
	if err != nil {
		return LoginOutcome{}, fmt.Errorf("identity: resolve login: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO guest_sessions
		(id, user_id, created_at, revoked) VALUES (?, ?, ?, 0)`,
		nextSession, target, now); err != nil {
		return LoginOutcome{}, fmt.Errorf("identity: resolve login: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		"UPDATE guest_sessions SET revoked = 1 WHERE id = ?", sessionID); err != nil {
		return LoginOutcome{}, fmt.Errorf("identity: resolve login: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return LoginOutcome{}, fmt.Errorf("identity: resolve login: %w", err)
	}
	committed = true
	return LoginOutcome{UserID: target, SessionID: nextSession, Switched: switched}, nil
}

// GoogleHandler serves the Google sign-in pair on one handler. The
// route table mounts it behind the guest middleware, so every request
// carries a user and a session. The start redirects to the provider,
// and the callback validates the answer and redirects to the static
// outcome page with its flag.
func (s *Service) GoogleHandler(cfg GoogleConfig) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/login/google/start", func(w http.ResponseWriter, r *http.Request) {
		s.handleGoogleStart(w, r, cfg)
	})
	mux.HandleFunc("GET /api/login/google/callback", func(w http.ResponseWriter, r *http.Request) {
		s.handleGoogleCallback(w, r, cfg)
	})
	return mux
}

// googleRedirect answers a 303 to the static outcome page. Every login
// answer travels no-store, so a shared cache never keeps it.
func googleRedirect(w http.ResponseWriter, r *http.Request, location string) {
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, location, http.StatusSeeOther)
}

// handleGoogleStart answers the start route with the provider
// redirect. A wiring fault answers 500, so a missing client surfaces
// instead of hiding.
func (s *Service) handleGoogleStart(w http.ResponseWriter, r *http.Request, cfg GoogleConfig) {
	user, sessionID, ok := loginSession(w, r)
	if !ok {
		return
	}
	target, err := s.StartGoogleLogin(r.Context(), cfg, sessionID, user.ID)
	if err != nil {
		_ = wire.WriteError(w, http.StatusInternalServerError, CodeInternal, "google sign-in is not ready", nil)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// handleGoogleCallback answers the provider return. A success rotates
// the session and lands on the done flag. A conflict lands on the
// conflict flag with the state, so the switch choice retries without
// a code. Any other refusal lands on the failure flag, and every
// answer travels no-store.
func (s *Service) handleGoogleCallback(w http.ResponseWriter, r *http.Request, cfg GoogleConfig) {
	user, sessionID, ok := loginSession(w, r)
	if !ok {
		return
	}
	query := r.URL.Query()
	if query.Get("error") != "" {
		googleRedirect(w, r, googleCallbackPage+"?error=signin_failed")
		return
	}
	outcome, err := s.CompleteGoogleLogin(r.Context(), cfg, sessionID, user.ID,
		query.Get("state"), query.Get("code"), query.Get("choice"))
	if err != nil {
		if errors.Is(err, ErrDiaryConflict) {
			googleRedirect(w, r, googleCallbackPage+"?conflict=1&state="+url.QueryEscape(query.Get("state")))
			return
		}
		googleRedirect(w, r, googleCallbackPage+"?error=signin_failed")
		return
	}
	s.SetSessionCookie(w, outcome.SessionID)
	googleRedirect(w, r, googleCallbackPage+"?done=1")
}
