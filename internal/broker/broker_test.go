package broker

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/nrynss/keel/cost"
	costsqlitestore "github.com/nrynss/keel/cost/sqlitestore"
	flagsqlitestore "github.com/nrynss/keel/flag/sqlitestore"
	"github.com/nrynss/keel/gate"
	"github.com/nrynss/keel/lease"
	leasesqlitestore "github.com/nrynss/keel/lease/sqlitestore"
	"github.com/nrynss/keel/sqlite"
	"github.com/nrynss/reprise/internal/assemblyai"
	"github.com/nrynss/reprise/internal/identity"
	"github.com/nrynss/reprise/internal/store"
)

const (
	testCapSeconds  = 1800
	testGuestMax    = 10
	testSigningKey  = "test-signing-key-for-broker-probes-only"
	testAPIKeyMark  = "key-sentinel-9z-broker-probe"
	testServerToken = "server-token-1"
)

// stubMinter counts mint calls and answers from its fields.
type stubMinter struct {
	calls int
	token string
	err   error
}

func (s *stubMinter) Mint(_ context.Context, _ int) (string, error) {
	s.calls++
	if s.err != nil {
		return "", s.err
	}
	return s.token, nil
}

// stubBuilder answers the session config from its fields.
type stubBuilder struct {
	config SessionConfig
	err    error
}

func (s stubBuilder) BuildSessionConfig(_ context.Context, _ string) (SessionConfig, error) {
	if s.err != nil {
		return SessionConfig{}, s.err
	}
	return s.config, nil
}

// failDiary runs every read against the real diary and fails the write, so
// the store failure path pins with live budget and lease stores.
type failDiary struct {
	Diary
}

func (failDiary) CreateEpisodeAndSession(_ context.Context, _ string, _ int) (string, string, error) {
	return "", "", errors.New("failDiary: forced session write failure")
}

type fixtureOptions struct {
	guestMax    int
	globalLimit cost.Price
	ownerLimit  cost.Price
	quotaSlots  int
	minter      TokenMinter
	builder     ConfigBuilder
	diary       Diary
}

type fixture struct {
	t          *testing.T
	db         *sqlite.DB
	broker     *Broker
	users      *identity.Service
	flags      *flagsqlitestore.Store
	costs      *costsqlitestore.Store
	budgets    *costsqlitestore.KeyedBudget
	leaseStore *leasesqlitestore.Store
	quota      *lease.Quota
	minter     *stubMinter
	builder    ConfigBuilder
	diary      Diary
	chain      http.Handler
}

func testEstimate(t *testing.T) cost.Price {
	t.Helper()
	estimate, err := estimateForCap(testCapSeconds)
	if err != nil {
		t.Fatalf("price test cap: %v", err)
	}
	return estimate
}

func newFixture(t *testing.T, opts *fixtureOptions) *fixture {
	t.Helper()
	estimate := testEstimate(t)
	var o fixtureOptions
	if opts != nil {
		o = *opts
	}
	if o.guestMax <= 0 {
		o.guestMax = testGuestMax
	}
	if o.globalLimit <= 0 {
		o.globalLimit = estimate * 100
	}
	if o.ownerLimit <= 0 {
		o.ownerLimit = estimate * 100
	}
	if o.quotaSlots <= 0 {
		o.quotaSlots = 10
	}
	minter := &stubMinter{token: "stub-token-1"}
	if o.minter != nil {
		if stub, ok := o.minter.(*stubMinter); ok {
			minter = stub
		}
	}
	builder := ConfigBuilder(stubBuilder{config: SessionConfig{
		SystemPrompt: "Speak warmly and ask one question at a time.",
		Greeting:     "Last time you mentioned the lantern. Did you ever light it?",
		Keyterms:     []string{"Mara", "Quilby"},
	}})
	if o.builder != nil {
		builder = o.builder
	}

	quiet := slog.New(slog.DiscardHandler)
	db, err := sqlite.Open(t.Context(), sqlite.Config{Path: t.TempDir() + "/broker.sqlite", Logger: quiet})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := store.Open(t.Context(), db); err != nil {
		t.Fatalf("open diary schema: %v", err)
	}
	users, err := identity.New(t.Context(), identity.Config{DB: db, SigningKey: testSigningKey})
	if err != nil {
		t.Fatalf("open identity: %v", err)
	}
	flags, err := flagsqlitestore.Open(t.Context(), flagsqlitestore.Config{DB: db})
	if err != nil {
		t.Fatalf("open flags: %v", err)
	}
	costs, err := costsqlitestore.Open(t.Context(), costsqlitestore.Config{DB: db, Limit: o.globalLimit})
	if err != nil {
		t.Fatalf("open cost store: %v", err)
	}
	budgets := costsqlitestore.NewKeyedBudget(costs)
	leases, err := leasesqlitestore.Open(t.Context(), leasesqlitestore.Config{DB: db})
	if err != nil {
		t.Fatalf("open lease store: %v", err)
	}
	quota, err := lease.NewQuota(o.quotaSlots)
	if err != nil {
		t.Fatalf("open lease quota: %v", err)
	}
	diary := o.diary
	if diary == nil {
		diary, err = NewSQLiteDiary(db)
		if err != nil {
			t.Fatalf("open diary: %v", err)
		}
	}
	var minterFace TokenMinter = minter
	if o.minter != nil {
		minterFace = o.minter
	}
	broker, err := New(Config{
		Flags:             flags,
		Budgets:           budgets,
		DB:                db,
		LeaseQuota:        quota,
		LeaseStore:        leases,
		Minter:            minterFace,
		Sessions:          builder,
		Diary:             diary,
		SessionCapSeconds: testCapSeconds,
		GuestMaxSessions:  o.guestMax,
		OwnerSessionLimit: o.ownerLimit,
	})
	if err != nil {
		t.Fatalf("new broker: %v", err)
	}
	return &fixture{
		t:          t,
		db:         db,
		broker:     broker,
		users:      users,
		flags:      flags,
		costs:      costs,
		budgets:    budgets,
		leaseStore: leases,
		quota:      quota,
		minter:     minter,
		builder:    builder,
		diary:      diary,
		chain:      users.Middleware(broker),
	}
}

type postResult struct {
	rec    *httptest.ResponseRecorder
	cookie *http.Cookie
}

func (f *fixture) post(cookie *http.Cookie) postResult {
	f.t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/sessions", nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	f.chain.ServeHTTP(rec, req)
	out := postResult{rec: rec}
	for _, c := range rec.Result().Cookies() {
		if c.Name == identity.CookieName {
			got := *c
			out.cookie = &got
		}
	}
	if out.cookie == nil {
		out.cookie = cookie
	}
	return out
}

type errorEnvelope struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func refusalCode(t *testing.T, rec *httptest.ResponseRecorder) (int, string) {
	t.Helper()
	var body errorEnvelope
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode refusal: %v", err)
	}
	if body.Error.Code == "" || body.Error.Message == "" {
		t.Fatalf("refusal carries no code or message: %+v", body)
	}
	return rec.Code, body.Error.Code
}

type sessionBody struct {
	SessionID                 string        `json:"session_id"`
	EpisodeID                 string        `json:"episode_id"`
	Token                     string        `json:"token"`
	ExpiresInSeconds          int           `json:"expires_in_seconds"`
	MaxSessionDurationSeconds int           `json:"max_session_duration_seconds"`
	Config                    SessionConfig `json:"config"`
}

func countRows(t *testing.T, db *sqlite.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.Reader().QueryRowContext(t.Context(), query, args...).Scan(&n); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	return n
}

func heldReservations(t *testing.T, costs *costsqlitestore.Store) cost.Price {
	t.Helper()
	held, err := costs.Reserved(t.Context())
	if err != nil {
		t.Fatalf("read held reservations: %v", err)
	}
	return held
}

func ownerOfFirstUser(t *testing.T, db *sqlite.DB) string {
	t.Helper()
	var id string
	if err := db.Reader().QueryRowContext(t.Context(), "SELECT id FROM users LIMIT 1").Scan(&id); err != nil {
		t.Fatalf("read owner: %v", err)
	}
	return id
}

func TestPassMintsOneOfEverything(t *testing.T) {
	var calls int
	var gotQuery url.Values
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		gotQuery = r.URL.Query()
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"token":"` + testServerToken + `"}`))
	}))
	defer server.Close()
	transport := server.Client()
	realClient, err := assemblyai.NewClient(server.URL, testAPIKeyMark, transport)
	if err != nil {
		t.Fatalf("new token client: %v", err)
	}
	fx := newFixture(t, &fixtureOptions{minter: realClient})

	got := fx.post(nil)
	if got.rec.Code != http.StatusCreated {
		t.Fatalf("status %d, want 201: %s", got.rec.Code, got.rec.Body.String())
	}
	raw := got.rec.Body.String()
	if strings.Contains(raw, testAPIKeyMark) {
		t.Fatalf("response body carries the provider key")
	}
	var body sessionBody
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatalf("decode session: %v", err)
	}
	if body.Token != testServerToken {
		t.Fatalf("token %q, want %q", body.Token, testServerToken)
	}
	if body.SessionID == "" || body.EpisodeID == "" {
		t.Fatalf("session or episode id is empty: %+v", body)
	}
	if body.ExpiresInSeconds != assemblyai.TokenExpirySeconds {
		t.Fatalf("expiry %d, want %d", body.ExpiresInSeconds, assemblyai.TokenExpirySeconds)
	}
	if body.MaxSessionDurationSeconds != testCapSeconds {
		t.Fatalf("cap %d, want %d", body.MaxSessionDurationSeconds, testCapSeconds)
	}
	if body.Config.Greeting == "" || body.Config.SystemPrompt == "" || len(body.Config.Keyterms) != 2 {
		t.Fatalf("config did not echo the builder: %+v", body.Config)
	}
	if calls != 1 {
		t.Fatalf("token calls %d, want exactly 1", calls)
	}
	if gotQuery.Get("expires_in_seconds") != "60" {
		t.Fatalf("token expiry param %q, want 60", gotQuery.Get("expires_in_seconds"))
	}
	if gotQuery.Get("max_session_duration_seconds") != "1800" {
		t.Fatalf("token cap param %q, want 1800", gotQuery.Get("max_session_duration_seconds"))
	}
	if gotAuth != testAPIKeyMark {
		t.Fatalf("token call missed the provider key")
	}

	owner := ownerOfFirstUser(t, fx.db)
	var tokenCap, connected int
	var providerID, rowOwner, rowEpisode string
	err = fx.db.Reader().QueryRowContext(t.Context(),
		`SELECT owner_id, episode_id, provider_session_id, token_cap, connected_seconds FROM sessions WHERE id = ?`,
		body.SessionID).Scan(&rowOwner, &rowEpisode, &providerID, &tokenCap, &connected)
	if err != nil {
		t.Fatalf("read session row: %v", err)
	}
	if rowOwner != owner || rowEpisode != body.EpisodeID {
		t.Fatalf("session row names %q/%q, want %q/%q", rowOwner, rowEpisode, owner, body.EpisodeID)
	}
	if tokenCap != testCapSeconds || connected != 0 {
		t.Fatalf("session row cap %d connected %d, want %d and 0", tokenCap, connected, testCapSeconds)
	}
	var state string
	err = fx.db.Reader().QueryRowContext(t.Context(),
		"SELECT state FROM episodes WHERE id = ?", body.EpisodeID).Scan(&state)
	if err != nil {
		t.Fatalf("read episode row: %v", err)
	}
	if state != "recording" {
		t.Fatalf("episode state %q, want recording", state)
	}
	if held := heldReservations(t, fx.costs); held != testEstimate(t) {
		t.Fatalf("held reservations %d, want exactly one estimate", held)
	}
	if active := fx.broker.Leases().Active(); active != 1 {
		t.Fatalf("open leases %d, want exactly 1", active)
	}
}

func TestGateRefusesBeforeAnythingMints(t *testing.T) {
	fx := newFixture(t, nil)
	g, err := gate.New(gate.Config{})
	if err != nil {
		t.Fatalf("new gate: %v", err)
	}
	rule := gate.Rule{
		Name:      "sessions",
		PerClient: gate.Limit{Burst: 1, Every: time.Hour},
		Global:    gate.Limit{Burst: 1, Every: time.Hour},
	}
	inner, err := fx.broker.Route(g, rule)
	if err != nil {
		t.Fatalf("route broker: %v", err)
	}
	fx.chain = fx.users.Middleware(inner)

	first := fx.post(nil)
	if first.rec.Code != http.StatusCreated {
		t.Fatalf("first status %d, want 201: %s", first.rec.Code, first.rec.Body.String())
	}
	second := fx.post(first.cookie)
	status, code := refusalCode(t, second.rec)
	if status != http.StatusTooManyRequests || code != "rate_limited" {
		t.Fatalf("gate refusal %d/%q, want 429/rate_limited", status, code)
	}
	if fx.minter.calls != 1 {
		t.Fatalf("token calls %d, want 1: the refusal minted", fx.minter.calls)
	}
	if n := countRows(t, fx.db, "SELECT COUNT(*) FROM sessions"); n != 1 {
		t.Fatalf("session rows %d, want 1: the refusal wrote", n)
	}
	if held := heldReservations(t, fx.costs); held != testEstimate(t) {
		t.Fatalf("held reservations %d, want the first hold only", held)
	}
}

func TestKillSwitchRefusesAtRequestTime(t *testing.T) {
	fx := newFixture(t, nil)
	if _, err := fx.flags.SetBool(t.Context(), KillSwitch, true); err != nil {
		t.Fatalf("set kill switch: %v", err)
	}
	got := fx.post(nil)
	status, code := refusalCode(t, got.rec)
	if status != http.StatusServiceUnavailable || code != CodePaused {
		t.Fatalf("kill refusal %d/%q, want 503/%s", status, code, CodePaused)
	}
	if fx.minter.calls != 0 {
		t.Fatalf("token calls %d, want 0", fx.minter.calls)
	}
	if n := countRows(t, fx.db, "SELECT COUNT(*) FROM sessions"); n != 0 {
		t.Fatalf("session rows %d, want 0", n)
	}
	if held := heldReservations(t, fx.costs); held != 0 {
		t.Fatalf("held reservations %d, want 0", held)
	}
}

func TestGuestQuotaRefuses(t *testing.T) {
	fx := newFixture(t, &fixtureOptions{guestMax: 1})
	first := fx.post(nil)
	if first.rec.Code != http.StatusCreated {
		t.Fatalf("first status %d, want 201: %s", first.rec.Code, first.rec.Body.String())
	}
	second := fx.post(first.cookie)
	status, code := refusalCode(t, second.rec)
	if status != http.StatusForbidden || code != CodeQuota {
		t.Fatalf("quota refusal %d/%q, want 403/%s", status, code, CodeQuota)
	}
	if fx.minter.calls != 1 {
		t.Fatalf("token calls %d, want 1", fx.minter.calls)
	}
	if n := countRows(t, fx.db, "SELECT COUNT(*) FROM sessions"); n != 1 {
		t.Fatalf("session rows %d, want 1", n)
	}
	if held := heldReservations(t, fx.costs); held != testEstimate(t) {
		t.Fatalf("held reservations %d, want the first hold only", held)
	}
}

func TestOwnerCeilingRefuses(t *testing.T) {
	estimate := testEstimate(t)
	fx := newFixture(t, &fixtureOptions{ownerLimit: estimate - 1})
	got := fx.post(nil)
	status, code := refusalCode(t, got.rec)
	if status != http.StatusServiceUnavailable || code != CodeBudget {
		t.Fatalf("owner refusal %d/%q, want 503/%s", status, code, CodeBudget)
	}
	if fx.minter.calls != 0 {
		t.Fatalf("token calls %d, want 0", fx.minter.calls)
	}
	if n := countRows(t, fx.db, "SELECT COUNT(*) FROM sessions"); n != 0 {
		t.Fatalf("session rows %d, want 0", n)
	}
	if held := heldReservations(t, fx.costs); held != 0 {
		t.Fatalf("held reservations %d, want 0", held)
	}
	if active := fx.broker.Leases().Active(); active != 0 {
		t.Fatalf("open leases %d, want 0", active)
	}
}

func TestGlobalCeilingRefuses(t *testing.T) {
	estimate := testEstimate(t)
	fx := newFixture(t, &fixtureOptions{globalLimit: estimate - 1})
	got := fx.post(nil)
	status, code := refusalCode(t, got.rec)
	if status != http.StatusServiceUnavailable || code != CodeBudget {
		t.Fatalf("global refusal %d/%q, want 503/%s", status, code, CodeBudget)
	}
	if fx.minter.calls != 0 {
		t.Fatalf("token calls %d, want 0", fx.minter.calls)
	}
	if n := countRows(t, fx.db, "SELECT COUNT(*) FROM sessions"); n != 0 {
		t.Fatalf("session rows %d, want 0", n)
	}
	if held := heldReservations(t, fx.costs); held != 0 {
		t.Fatalf("held reservations %d, want 0", held)
	}
}

func TestLeaseSlotsRefuse(t *testing.T) {
	fx := newFixture(t, &fixtureOptions{quotaSlots: 1})
	first := fx.post(nil)
	if first.rec.Code != http.StatusCreated {
		t.Fatalf("first status %d, want 201: %s", first.rec.Code, first.rec.Body.String())
	}
	second := fx.post(first.cookie)
	status, code := refusalCode(t, second.rec)
	if status != http.StatusTooManyRequests || code != CodeSlots {
		t.Fatalf("slot refusal %d/%q, want 429/%s", status, code, CodeSlots)
	}
	if fx.minter.calls != 1 {
		t.Fatalf("token calls %d, want 1", fx.minter.calls)
	}
	if n := countRows(t, fx.db, "SELECT COUNT(*) FROM sessions"); n != 1 {
		t.Fatalf("session rows %d, want 1", n)
	}
	if held := heldReservations(t, fx.costs); held != testEstimate(t) {
		t.Fatalf("held reservations %d, want the first hold only", held)
	}
	if active := fx.broker.Leases().Active(); active != 1 {
		t.Fatalf("open leases %d, want 1", active)
	}
}

func TestTokenFailureReleasesEverything(t *testing.T) {
	fx := newFixture(t, &fixtureOptions{minter: &stubMinter{err: errors.New("stubMinter: forced mint failure")}})
	got := fx.post(nil)
	status, code := refusalCode(t, got.rec)
	if status != http.StatusBadGateway || code != CodeProvider {
		t.Fatalf("provider refusal %d/%q, want 502/%s", status, code, CodeProvider)
	}
	if n := countRows(t, fx.db, "SELECT COUNT(*) FROM sessions"); n != 0 {
		t.Fatalf("session rows %d, want 0", n)
	}
	if n := countRows(t, fx.db, "SELECT COUNT(*) FROM episodes"); n != 0 {
		t.Fatalf("episode rows %d, want 0", n)
	}
	if held := heldReservations(t, fx.costs); held != 0 {
		t.Fatalf("held reservations %d, want 0", held)
	}
	if active := fx.broker.Leases().Active(); active != 0 {
		t.Fatalf("open leases %d, want 0", active)
	}
}

func TestConfigFailureReleasesEverything(t *testing.T) {
	fx := newFixture(t, &fixtureOptions{
		builder: stubBuilder{err: errors.New("stubBuilder: forced config failure")},
	})
	got := fx.post(nil)
	status, code := refusalCode(t, got.rec)
	if status != http.StatusInternalServerError || code != CodeInternal {
		t.Fatalf("config refusal %d/%q, want 500/%s", status, code, CodeInternal)
	}
	if fx.minter.calls != 1 {
		t.Fatalf("token calls %d, want 1 minted then cleaned", fx.minter.calls)
	}
	if n := countRows(t, fx.db, "SELECT COUNT(*) FROM sessions"); n != 0 {
		t.Fatalf("session rows %d, want 0", n)
	}
	if held := heldReservations(t, fx.costs); held != 0 {
		t.Fatalf("held reservations %d, want 0", held)
	}
	if active := fx.broker.Leases().Active(); active != 0 {
		t.Fatalf("open leases %d, want 0", active)
	}
}

func TestStoreFailureReleasesEverything(t *testing.T) {
	fx := newFixture(t, nil)
	rebuilt, err := New(Config{
		Flags:             fx.flags,
		Budgets:           fx.budgets,
		DB:                fx.db,
		LeaseQuota:        mustQuota(t, 10),
		LeaseStore:        fx.leaseStore,
		Minter:            fx.minter,
		Sessions:          fx.builder,
		Diary:             failDiary{Diary: fx.diary},
		SessionCapSeconds: testCapSeconds,
		GuestMaxSessions:  testGuestMax,
		OwnerSessionLimit: testEstimate(t) * 100,
	})
	if err != nil {
		t.Fatalf("rebuild broker: %v", err)
	}
	fx.broker = rebuilt
	fx.chain = fx.users.Middleware(rebuilt)

	got := fx.post(nil)
	status, code := refusalCode(t, got.rec)
	if status != http.StatusInternalServerError || code != CodeInternal {
		t.Fatalf("store refusal %d/%q, want 500/%s", status, code, CodeInternal)
	}
	if fx.minter.calls != 1 {
		t.Fatalf("token calls %d, want 1 minted then cleaned", fx.minter.calls)
	}
	if n := countRows(t, fx.db, "SELECT COUNT(*) FROM sessions"); n != 0 {
		t.Fatalf("session rows %d, want 0", n)
	}
	if n := countRows(t, fx.db, "SELECT COUNT(*) FROM episodes"); n != 0 {
		t.Fatalf("episode rows %d, want 0", n)
	}
	if held := heldReservations(t, fx.costs); held != 0 {
		t.Fatalf("held reservations %d, want 0", held)
	}
	if active := rebuilt.Leases().Active(); active != 0 {
		t.Fatalf("open leases %d, want 0", active)
	}
}

func TestAnonymousRequestRefuses(t *testing.T) {
	fx := newFixture(t, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/sessions", nil)
	rec := httptest.NewRecorder()
	fx.broker.ServeHTTP(rec, req)
	status, code := refusalCode(t, rec)
	if status != http.StatusUnauthorized || code != CodeNoOwner {
		t.Fatalf("anonymous refusal %d/%q, want 401/%s", status, code, CodeNoOwner)
	}
}

func TestProviderErrorBodyCarriesNoKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"overloaded"}`))
	}))
	defer server.Close()
	realClient, err := assemblyai.NewClient(server.URL, testAPIKeyMark, server.Client())
	if err != nil {
		t.Fatalf("new token client: %v", err)
	}
	fx := newFixture(t, &fixtureOptions{minter: realClient})
	got := fx.post(nil)
	status, code := refusalCode(t, got.rec)
	if status != http.StatusBadGateway || code != CodeProvider {
		t.Fatalf("provider refusal %d/%q, want 502/%s", status, code, CodeProvider)
	}
	if strings.Contains(got.rec.Body.String(), testAPIKeyMark) {
		t.Fatalf("provider refusal carries the key")
	}
	if held := heldReservations(t, fx.costs); held != 0 {
		t.Fatalf("held reservations %d, want 0", held)
	}
}

func TestRouteNeedsGate(t *testing.T) {
	fx := newFixture(t, nil)
	if _, err := fx.broker.Route(nil, gate.Rule{}); err == nil {
		t.Fatalf("route with nil gate succeeded, want an error")
	}
}

func TestNewRefusesBadConfig(t *testing.T) {
	fx := newFixture(t, nil)
	good := Config{
		Flags:             fx.flags,
		Budgets:           fx.budgets,
		DB:                fx.db,
		LeaseQuota:        mustQuota(t, 10),
		LeaseStore:        fx.leaseStore,
		Minter:            fx.minter,
		Sessions:          fx.builder,
		Diary:             fx.diary,
		SessionCapSeconds: testCapSeconds,
		GuestMaxSessions:  testGuestMax,
		OwnerSessionLimit: testEstimate(t),
	}
	bad := []Config{
		{},
		func() Config { c := good; c.Flags = nil; return c }(),
		func() Config { c := good; c.Budgets = nil; return c }(),
		func() Config { c := good; c.DB = nil; return c }(),
		func() Config { c := good; c.LeaseQuota = nil; return c }(),
		func() Config { c := good; c.LeaseStore = nil; return c }(),
		func() Config { c := good; c.Minter = nil; return c }(),
		func() Config { c := good; c.Sessions = nil; return c }(),
		func() Config { c := good; c.Diary = nil; return c }(),
		func() Config { c := good; c.SessionCapSeconds = 59; return c }(),
		func() Config { c := good; c.SessionCapSeconds = 10801; return c }(),
		func() Config { c := good; c.GuestMaxSessions = 0; return c }(),
		func() Config { c := good; c.OwnerSessionLimit = 0; return c }(),
	}
	for i, cfg := range bad {
		if _, err := New(cfg); err == nil {
			t.Fatalf("config %d built, want an error", i)
		}
	}
	if _, err := New(good); err != nil {
		t.Fatalf("good config refused: %v", err)
	}
}

func mustQuota(t *testing.T, n int) *lease.Quota {
	t.Helper()
	quota, err := lease.NewQuota(n)
	if err != nil {
		t.Fatalf("new quota: %v", err)
	}
	return quota
}
