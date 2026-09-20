package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/nrynss/keel/cost"
	costsqlitestore "github.com/nrynss/keel/cost/sqlitestore"
	flagsqlitestore "github.com/nrynss/keel/flag/sqlitestore"
	"github.com/nrynss/keel/gate"
	keelid "github.com/nrynss/keel/id"
	"github.com/nrynss/keel/job"
	jobsqlitestore "github.com/nrynss/keel/job/sqlitestore"
	"github.com/nrynss/keel/lease"
	leasesqlitestore "github.com/nrynss/keel/lease/sqlitestore"
	"github.com/nrynss/keel/mediastore"
	mediasqlitestore "github.com/nrynss/keel/mediastore/sqlitestore"
	keelsqlite "github.com/nrynss/keel/sqlite"
	"github.com/nrynss/keel/stream"
	"github.com/nrynss/keel/upload"
	"github.com/nrynss/reprise/internal/api"
	"github.com/nrynss/reprise/internal/assemblyai"
	"github.com/nrynss/reprise/internal/broker"
	"github.com/nrynss/reprise/internal/cover"
	"github.com/nrynss/reprise/internal/episode"
	"github.com/nrynss/reprise/internal/gemini"
	"github.com/nrynss/reprise/internal/host"
	"github.com/nrynss/reprise/internal/identity"
	"github.com/nrynss/reprise/internal/memory"
	"github.com/nrynss/reprise/internal/render"
	"github.com/nrynss/reprise/internal/settings"
	reprisestore "github.com/nrynss/reprise/internal/store"
	"github.com/nrynss/reprise/internal/transcript"
	"google.golang.org/genai"
)

func TestHealthzNamesBuild(t *testing.T) {
	rec := httptest.NewRecorder()
	handleHealth(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("healthz status = %d, want 200", rec.Code)
	}
	body, _ := io.ReadAll(rec.Body)
	for _, want := range []string{"ok ", "version="} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("healthz body %q lacks %q", body, want)
		}
	}
}

func TestUnknownPathFallsBackToShell(t *testing.T) {
	dir := t.TempDir()
	shell := "<!doctype html><html><body>fallback shell</body></html>"
	if err := os.WriteFile(filepath.Join(dir, "fallback.html"), []byte(shell), 0o644); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(appHandler(dir))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/episodes/one")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unknown path status = %d, want 200 from the shell", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != shell {
		t.Fatalf("unknown path body = %q, want the shell", body)
	}
}

func TestKnownFileServedDirectly(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("shell"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app.js"), []byte("export {}"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(appHandler(dir))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/app.js")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "export {}" {
		t.Fatalf("asset body = %q, want the file contents", body)
	}
}

// stubHandler answers 200 through any gate, so a passed request is visible
// as a 200 and a refused one is not.
func stubHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func TestGatePassesSessionsBeforeDrain(t *testing.T) {
	gate := &drainingGate{handler: stubHandler()}

	rec := httptest.NewRecorder()
	gate.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/sessions", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("session start status = %d, want 200 before the drain", rec.Code)
	}
}

func TestGateRefusesSessionsDuringDrain(t *testing.T) {
	gate := &drainingGate{handler: stubHandler()}
	gate.draining.Store(true)

	rec := httptest.NewRecorder()
	gate.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/sessions", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("session start status = %d, want 503 during the drain", rec.Code)
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	raw, _ := io.ReadAll(rec.Body)
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("drain refusal body %q is not the shared envelope: %v", raw, err)
	}
	if body.Error.Code != "server_draining" {
		t.Fatalf("drain refusal code = %q, want server_draining", body.Error.Code)
	}
}

func TestGateLeavesOtherTrafficAlone(t *testing.T) {
	gate := &drainingGate{handler: stubHandler()}
	gate.draining.Store(true)

	for _, req := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/healthz", nil),
		httptest.NewRequest(http.MethodGet, "/api/sessions", nil),
		httptest.NewRequest(http.MethodPost, "/api/episodes/abc/done", nil),
	} {
		rec := httptest.NewRecorder()
		gate.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s %s status = %d, want 200 during the drain", req.Method, req.URL.Path, rec.Code)
		}
	}
}

// fakeSessions is a sessions registry the test opens and closes by hand.
type fakeSessions struct {
	mu   sync.Mutex
	open int
}

// Active reports how many sessions stay open.
func (f *fakeSessions) Active() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.open
}

// close marks every session ended.
func (f *fakeSessions) close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.open = 0
}

func TestWaitSessionsReturnsAtOnceOnNil(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	waitSessions(ctx, nil, time.Millisecond)
}

func TestWaitSessionsReturnsWhenEmpty(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	waitSessions(ctx, &fakeSessions{}, time.Millisecond)
}

func TestWaitSessionsWaitsForClose(t *testing.T) {
	reg := &fakeSessions{open: 2}
	go func() {
		time.Sleep(20 * time.Millisecond)
		reg.close()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	waitSessions(ctx, reg, time.Millisecond)
	if got := reg.Active(); got != 0 {
		t.Fatalf("open sessions = %d, want 0 after the drain", got)
	}
}

func TestWaitSessionsEndsOnTimeout(t *testing.T) {
	reg := &fakeSessions{open: 1}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	waitSessions(ctx, reg, time.Millisecond)
	if got := reg.Active(); got != 1 {
		t.Fatalf("open sessions = %d, want 1 after the budget ran out", got)
	}
}

func TestDrainBudgetFollowsSessionCap(t *testing.T) {
	got := drainBudget(settings.Settings{SessionMaxSeconds: 1800})
	if got != 1800*time.Second {
		t.Fatalf("drain budget = %s, want 30m0s", got)
	}
}

func TestDrainBudgetFallsBackWithoutCap(t *testing.T) {
	for _, cap := range []int{0, -5} {
		got := drainBudget(settings.Settings{SessionMaxSeconds: cap})
		if got != fallbackDrainSeconds*time.Second {
			t.Fatalf("drain budget with cap %d = %s, want %ds", cap, got, fallbackDrainSeconds)
		}
	}
}

func TestRunDrainsAndStops(t *testing.T) {
	gate := &drainingGate{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handleHealth)
	gate.handler = mux

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Addr: addr, Handler: gate}
	reg := &fakeSessions{open: 1}
	sigs := make(chan os.Signal, 1)

	done := make(chan error, 1)
	go func() {
		done <- run(srv, gate, reg, 10*time.Second, sigs)
	}()

	deadline := time.Now().Add(10 * time.Second)
	for {
		resp, err := http.Get("http://" + addr + "/healthz")
		if err == nil {
			resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("server never started: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}

	sigs <- syscall.SIGTERM
	reg.close()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run error = %v, want nil after a clean drain", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run never returned after the signal")
	}

	if !gate.draining.Load() {
		t.Fatal("gate never closed, want no new sessions after the signal")
	}
	if _, err := http.Get("http://" + addr + "/healthz"); err == nil {
		t.Fatal("server still answers, want the listener closed after the drain")
	}
}

func TestSpendCeilingConvertsCents(t *testing.T) {
	got, err := spendCeiling(2000)
	if err != nil {
		t.Fatalf("spendCeiling(2000) error = %v, want nil", err)
	}
	if want := cost.Price(2000) * nanosPerCent; got != want {
		t.Fatalf("spendCeiling(2000) = %d, want %d", got, want)
	}
	if _, err := spendCeiling(0); err != nil {
		t.Fatalf("spendCeiling(0) error = %v, want nil", err)
	}
	if _, err := spendCeiling(-1); err == nil {
		t.Fatal("spendCeiling(-1) error = nil, want a refusal")
	}
	if _, err := spendCeiling(math.MaxInt64); err == nil {
		t.Fatal("spendCeiling(max) error = nil, want an overflow refusal")
	}
}

func TestHostBuilderServesOpenerOnEmptySeason(t *testing.T) {
	ctx := context.Background()
	db, err := keelsqlite.Open(ctx, keelsqlite.Config{Path: filepath.Join(t.TempDir(), "test.db")})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	if _, err := reprisestore.Open(ctx, db); err != nil {
		t.Fatalf("migrate diary schema: %v", err)
	}
	got, err := hostBuilder{db: db.Writer()}.BuildSessionConfig(ctx, "owner-1")
	if err != nil {
		t.Fatalf("BuildSessionConfig error = %v, want nil", err)
	}
	want := host.Build(host.Input{})
	if got.Greeting != want.Greeting {
		t.Fatalf("greeting = %q, want %q", got.Greeting, want.Greeting)
	}
	if got.SystemPrompt != want.SystemPrompt {
		t.Fatalf("system prompt = %q, want %q", got.SystemPrompt, want.SystemPrompt)
	}
	if len(got.Keyterms) != 0 {
		t.Fatalf("keyterms = %v, want none on an empty season", got.Keyterms)
	}
}

// wireFixture carries the stores one wired process shares. Every test
// below boots the same shape wireAPI builds, minus secrets and the
// network, so the pins hold for the binary too.
type wireFixture struct {
	db      *keelsqlite.DB
	costs   *costsqlitestore.Store
	budgets *costsqlitestore.KeyedBudget
	media   *mediastore.Store
	pipe    *pipeline
	ceiling cost.Price
}

// scriptedGemini answers every model call from canned bodies. Chapters
// and marking return the JSON their schemas promise, and the image
// call returns PNG bytes behind an inline part.
func scriptedGemini() *gemini.Client {
	return gemini.NewTestClient(func(_ context.Context, _ string, contents []*genai.Content, config *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
		for _, modality := range config.ResponseModalities {
			if modality == "IMAGE" {
				return &genai.GenerateContentResponse{
					Candidates: []*genai.Candidate{{
						Content: &genai.Content{Role: "model", Parts: []*genai.Part{{
							InlineData: &genai.Blob{MIMEType: "image/png", Data: []byte{0x89, 0x50, 0x4e, 0x47}},
						}}},
					}},
				}, nil
			}
		}
		var prompt string
		if len(contents) > 0 && len(contents[0].Parts) > 0 {
			prompt = contents[0].Parts[0].Text
		}
		body := `{"title":"Harbor Light"}`
		switch {
		case strings.Contains(prompt, "Chapter the transcript"):
			body = `{"chapters":[{"title":"Dawn Ferry","start_ms":0}]}`
		case strings.Contains(prompt, "List the concrete things"):
			body = `{"commitments":[]}`
		case strings.Contains(prompt, "Decide whether"):
			body = `{"done":false,"quote":""}`
		}
		return &genai.GenerateContentResponse{
			Candidates: []*genai.Candidate{{
				Content:      &genai.Content{Role: "model", Parts: []*genai.Part{{Text: body}}},
				FinishReason: genai.FinishReasonStop,
			}},
			UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
				PromptTokenCount: 12, CandidatesTokenCount: 5, TotalTokenCount: 17,
			},
		}, nil
	})
}

// openWireFixture builds the diary, ledger, media, batch, and pipeline
// the binary wires, with a scripted model behind every Gemini call.
func openWireFixture(t *testing.T) *wireFixture {
	t.Helper()
	ctx := context.Background()
	db, err := keelsqlite.Open(ctx, keelsqlite.Config{Path: filepath.Join(t.TempDir(), "wire.db")})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := reprisestore.Open(ctx, db); err != nil {
		t.Fatalf("migrate diary schema: %v", err)
	}
	ceiling, err := spendCeiling(2000)
	if err != nil {
		t.Fatalf("spend ceiling: %v", err)
	}
	costs, err := costsqlitestore.Open(ctx, costsqlitestore.Config{DB: db, Limit: ceiling})
	if err != nil {
		t.Fatalf("open spend ledger: %v", err)
	}
	budgets := costsqlitestore.NewKeyedBudget(costs)
	mediaDir := t.TempDir()
	mediaIndex, err := mediasqlitestore.Open(ctx, mediasqlitestore.Config{DB: db})
	if err != nil {
		t.Fatalf("open media index: %v", err)
	}
	media, err := mediastore.Open(ctx, mediastore.Config{
		Dir:          mediaDir,
		Index:        mediaIndex,
		ContentTypes: mediaContentTypes,
	})
	if err != nil {
		t.Fatalf("open media store: %v", err)
	}
	batch, err := assemblyai.NewBatchClient(assemblyai.Config{APIKey: "probe-key", Model: "test-transcription"})
	if err != nil {
		t.Fatalf("open batch client: %v", err)
	}
	pipe := newPipeline(db, scriptedGemini(), batch, budgets, ceiling, "test-editorial",
		media, mediaDir, t.TempDir(), t.TempDir())
	return &wireFixture{db: db, costs: costs, budgets: budgets, media: media, pipe: pipe, ceiling: ceiling}
}

// fakeLeaseSettler stands in for the lease manager where the wiring
// test never opens a lease.
type fakeLeaseSettler struct{}

func (fakeLeaseSettler) Close(_ context.Context, id string, price cost.Price) (lease.Lease, error) {
	return lease.Lease{ID: id, State: lease.StateClosed, Settled: price}, nil
}

func (fakeLeaseSettler) Reconcile(_ context.Context, id string, provider cost.Price) (lease.Lease, error) {
	return lease.Lease{ID: id, State: lease.StateClosed, Settled: provider, Reconciled: true}, nil
}

func (fakeLeaseSettler) Inspect(_ context.Context, id string) (lease.Lease, error) {
	return lease.Lease{ID: id, State: lease.StateClosed}, nil
}

// fakeSessionReader replays one recorded provider close.
type fakeSessionReader struct {
	read broker.ProviderSession
}

func (f fakeSessionReader) ReadSession(context.Context, string) (broker.ProviderSession, error) {
	return f.read, nil
}

// fakeArtifacts refuses every download. Closes without recording URLs
// never fetch, so a call here fails the test.
type fakeArtifacts struct{ t *testing.T }

func (f fakeArtifacts) Fetch(context.Context, string) (io.ReadCloser, error) {
	f.t.Fatal("artifact fetch ran with no recording URL")
	return nil, errors.New("no fetch")
}

// fakeMediaWriter refuses every persist. Closes without recording URLs
// never persist, so a call here fails the test.
type fakeMediaWriter struct{ t *testing.T }

func (f fakeMediaWriter) Persist(context.Context, io.Reader, mediastore.Put) (string, error) {
	f.t.Fatal("media persist ran with no recording URL")
	return "", errors.New("no persist")
}

// openTestReconciler builds a reconciler over the fixture stores with a
// recorded provider close and no artifact traffic.
func openTestReconciler(t *testing.T, fx *wireFixture, diary broker.SessionStore, leases broker.LeaseSettler) *broker.Reconciler {
	t.Helper()
	rec, err := broker.NewReconciler(broker.ReconcilerConfig{
		DB:            fx.db,
		Sessions:      fakeSessionReader{read: broker.ProviderSession{ID: "prov-1", DurationSeconds: 372}},
		Artifacts:     fakeArtifacts{t: t},
		Budgets:       fx.budgets,
		Leases:        leases,
		Diary:         diary,
		Media:         fakeMediaWriter{t: t},
		MarginSeconds: broker.DefaultMarginSeconds,
	})
	if err != nil {
		t.Fatalf("open reconciler: %v", err)
	}
	return rec
}

// stubSource lists no open sessions. The kinds test never sweeps,
// so the source stays empty while the config seam stays filled.
type stubSource struct{}

func (stubSource) ListOpen(context.Context) ([]broker.Candidate, error) {
	return nil, nil
}

// TestPipelineKindsShareOneClientWithSettingsModels boots the wired
// pipeline shape and proves every paid kind registers non-idempotent
// with no resume, while the free kinds resume. It proves one Gemini
// client backs editorial, chapters, cover, and marking alike, and
// every model id arrives from settings.
func TestPipelineKindsShareOneClientWithSettingsModels(t *testing.T) {
	fx := openWireFixture(t)
	resolver := &render.Resolver{DB: fx.db.Writer(), WorkDir: t.TempDir(), Locate: fx.pipe.locateStems}
	rec := openTestReconciler(t, fx, fakeDiary{db: fx.db}, fakeLeaseSettler{})
	sweeper, err := broker.NewSweeper(broker.SweeperConfig{
		DB: fx.db, Source: stubSource{}, Statuses: fakeStatuses{},
		Ender: fakeEnder{}, Reconciler: rec,
	})
	if err != nil {
		t.Fatalf("open sweeper: %v", err)
	}
	kinds := fx.pipe.kinds(resolver, rec, sweeper)
	if len(kinds) != 8 {
		t.Fatalf("kinds = %d, want 8 pipeline and settle kinds", len(kinds))
	}
	for _, name := range []string{kindEditTranscript, kindEditorial, kindAnalysis, kindCover, kindMemory} {
		kind, ok := kinds[name]
		if !ok {
			t.Fatalf("kind %q is not registered", name)
		}
		if kind.Idempotent || kind.Resume != nil {
			t.Fatalf("kind %q resumes, want interrupted with no rerun", name)
		}
	}
	for _, name := range []string{kindRender, broker.KindName, broker.SweepKindName} {
		kind, ok := kinds[name]
		if !ok {
			t.Fatalf("kind %q is not registered", name)
		}
		if !kind.Idempotent || kind.Resume == nil {
			t.Fatalf("kind %q does not resume, want the idempotent path", name)
		}
	}
	if fx.pipe.editorialModel != "test-editorial" {
		t.Fatalf("editorial model = %q, want the settings value", fx.pipe.editorialModel)
	}
	if fx.pipe.chapters.client != fx.pipe.gemini {
		t.Fatal("chapters use a different client than editorial")
	}
	if fx.pipe.coverModel.client != fx.pipe.gemini {
		t.Fatal("cover uses a different client than editorial")
	}
	if fx.pipe.memoryModel.client != fx.pipe.gemini {
		t.Fatal("marking uses a different client than editorial")
	}
	if fx.pipe.batch.Model() != "test-transcription" {
		t.Fatalf("batch model = %q, want the settings value", fx.pipe.batch.Model())
	}
}

// fakeDiary writes settled durations on the real sessions table.
type fakeDiary struct {
	db *keelsqlite.DB
}

func (d fakeDiary) SetConnectedSeconds(ctx context.Context, sessionID string, connectedSeconds int) error {
	_, err := d.db.Writer().ExecContext(ctx,
		`UPDATE sessions SET connected_seconds = ? WHERE id = ?`, connectedSeconds, sessionID)
	return err
}

// fakeStatuses refuses every status read. The settle path never lists,
// so a call here fails the test.
type fakeStatuses struct{}

func (fakeStatuses) ReadStatus(context.Context, string) (broker.ProviderStatus, error) {
	return broker.ProviderStatus{}, errors.New("no status read")
}

// fakeEnder refuses every delete. The settle path never ends, so a
// call here fails the test.
type fakeEnder struct{}

func (fakeEnder) EndSession(context.Context, string) (broker.EndResult, error) {
	return broker.EndResult{}, errors.New("no end")
}

// wireMinter answers one provider token. The settle path never mints,
// so the broker only needs the seam filled.
type wireMinter struct{}

func (wireMinter) Mint(context.Context, int) (string, error) { return "wire-token", nil }

// wireBuilder answers an empty session config from stored rows.
type wireBuilder struct{}

func (wireBuilder) BuildSessionConfig(context.Context, string) (broker.SessionConfig, error) {
	return broker.SessionConfig{}, nil
}

// insertWireUser stores one guest owner for mint and settle rows.
func insertWireUser(t *testing.T, fx *wireFixture, owner string) {
	t.Helper()
	now := time.Now().UnixMilli()
	if _, err := fx.db.Writer().ExecContext(t.Context(),
		`INSERT INTO users (id, kind, created_at, last_seen_at) VALUES (?, 'guest', ?, ?)`,
		owner, now, now); err != nil {
		t.Fatalf("insert owner: %v", err)
	}
	if err := fx.budgets.SetLimit(t.Context(), owner, fx.ceiling); err != nil {
		t.Fatalf("set owner ceiling: %v", err)
	}
}

// openWireBroker builds the session broker over the fixture stores,
// with a stub minter and an empty session config.
func openWireBroker(t *testing.T, fx *wireFixture) *broker.Broker {
	t.Helper()
	flags, err := flagsqlitestore.Open(t.Context(), flagsqlitestore.Config{DB: fx.db})
	if err != nil {
		t.Fatalf("open flags: %v", err)
	}
	quota, err := lease.NewQuota(8)
	if err != nil {
		t.Fatalf("new quota: %v", err)
	}
	leaseStore, err := leasesqlitestore.Open(t.Context(), leasesqlitestore.Config{DB: fx.db})
	if err != nil {
		t.Fatalf("open lease store: %v", err)
	}
	diary, err := broker.NewSQLiteDiary(fx.db)
	if err != nil {
		t.Fatalf("open diary: %v", err)
	}
	sessionBroker, err := broker.New(broker.Config{
		Flags:             flags,
		Budgets:           fx.budgets,
		DB:                fx.db,
		LeaseQuota:        quota,
		LeaseStore:        leaseStore,
		Minter:            wireMinter{},
		Sessions:          wireBuilder{},
		Diary:             diary,
		SessionCapSeconds: 1800,
		GuestMaxSessions:  10,
		OwnerSessionLimit: fx.ceiling,
	})
	if err != nil {
		t.Fatalf("open broker: %v", err)
	}
	return sessionBroker
}

// mintWireSession starts one session through the broker middleware and
// returns its ids. The first call mints the guest behind the cookie.
func mintWireSession(t *testing.T, fx *wireFixture, sessionBroker *broker.Broker) (broker.Session, *http.Cookie) {
	t.Helper()
	identitySvc, err := identity.New(t.Context(), identity.Config{DB: fx.db, SigningKey: "wire-test-signing-key"})
	if err != nil {
		t.Fatalf("open identity: %v", err)
	}
	chain := identitySvc.Middleware(sessionBroker)
	req := httptest.NewRequest(http.MethodPost, "/api/sessions", nil)
	rec := httptest.NewRecorder()
	chain.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("mint status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	var session broker.Session
	if err := json.Unmarshal(rec.Body.Bytes(), &session); err != nil {
		t.Fatalf("decode session: %v", err)
	}
	var cookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		cookie = c
	}
	return session, cookie
}

// wireSpent reads the booked owner spend.
func wireSpent(t *testing.T, fx *wireFixture, owner string) cost.Price {
	t.Helper()
	var spent int64
	if err := fx.db.Reader().QueryRowContext(t.Context(),
		`SELECT spent_nd FROM cost_owner_budget WHERE owner = ?`, owner).Scan(&spent); err != nil {
		t.Fatalf("read owner spent: %v", err)
	}
	return cost.Price(spent)
}

// wireHeld reads the held owner reservations.
func wireHeld(t *testing.T, fx *wireFixture) cost.Price {
	t.Helper()
	held, err := fx.costs.Reserved(t.Context())
	if err != nil {
		t.Fatalf("read held: %v", err)
	}
	return held
}

// wireConnected reads the settled session duration.
func wireConnected(t *testing.T, fx *wireFixture, sessionID string) int {
	t.Helper()
	var seconds int
	if err := fx.db.Reader().QueryRowContext(t.Context(),
		`SELECT connected_seconds FROM sessions WHERE id = ?`, sessionID).Scan(&seconds); err != nil {
		t.Fatalf("read connected seconds: %v", err)
	}
	return seconds
}

// TestSettleEndSettlesExactlyOnce mints through the broker, records
// the provider close the end handler stores, and settles through the
// wired reconciler. The first settle books the recorded duration and
// frees the hold. The repeat settles nothing, so the reservation
// clears exactly once.
func TestSettleEndSettlesExactlyOnce(t *testing.T) {
	fx := openWireFixture(t)
	sessionBroker := openWireBroker(t, fx)
	diary, err := broker.NewSQLiteDiary(fx.db)
	if err != nil {
		t.Fatalf("open diary: %v", err)
	}
	rec := openTestReconciler(t, fx, diary, sessionBroker.Leases())

	session, cookie := mintWireSession(t, fx, sessionBroker)
	if cookie == nil {
		t.Fatal("mint set no guest cookie")
	}
	var owner string
	if err := fx.db.Reader().QueryRowContext(t.Context(),
		`SELECT owner_id FROM sessions WHERE id = ?`, session.SessionID).Scan(&owner); err != nil {
		t.Fatalf("read session owner: %v", err)
	}
	if got := wireHeld(t, fx); got <= 0 {
		t.Fatalf("held = %s, want the mint hold", got)
	}
	if _, err := fx.db.Writer().ExecContext(t.Context(),
		`UPDATE sessions SET provider_session_id = ? WHERE id = ?`, "prov-1", session.SessionID); err != nil {
		t.Fatalf("record provider close: %v", err)
	}
	in, err := sessionBroker.SettleInput(t.Context(), session.SessionID)
	if err != nil {
		t.Fatalf("settle input: %v", err)
	}
	res, err := rec.Reconcile(t.Context(), in)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if res.ConnectedSeconds != 372 {
		t.Fatalf("connected = %d, want the recorded 372", res.ConnectedSeconds)
	}
	const want = cost.Price(372 * 1250000)
	if res.Cost != want {
		t.Fatalf("cost = %s, want %s", res.Cost, want)
	}
	if got := wireSpent(t, fx, owner); got != want {
		t.Fatalf("spent = %s, want %s booked once", got, want)
	}
	if got := wireHeld(t, fx); got != 0 {
		t.Fatalf("held = %s, want no hold after settle", got)
	}
	if got := wireConnected(t, fx, session.SessionID); got != 372 {
		t.Fatalf("session duration = %d, want 372", got)
	}
	again, err := rec.Reconcile(t.Context(), in)
	if err != nil {
		t.Fatalf("repeat reconcile: %v", err)
	}
	if again.ConnectedSeconds != 372 {
		t.Fatalf("repeat connected = %d, want 372", again.ConnectedSeconds)
	}
	if got := wireSpent(t, fx, owner); got != want {
		t.Fatalf("spent after repeat = %s, want still %s", got, want)
	}
}

// TestRestartFailsInterruptedPaidJobs leaves a paid job running, then
// reopens the runner the way a restart does. Recovery marks the job
// interrupted, and the restart pass fails its episode for an explicit
// retry instead of rerunning the paid call.
func TestRestartFailsInterruptedPaidJobs(t *testing.T) {
	fx := openWireFixture(t)
	const owner = "owner-restart-fail"
	insertWireUser(t, fx, owner)
	diary, err := broker.NewSQLiteDiary(fx.db)
	if err != nil {
		t.Fatalf("open diary: %v", err)
	}
	episodeID, _, err := diary.CreateEpisodeAndSession(t.Context(), owner, 1800)
	if err != nil {
		t.Fatalf("create episode: %v", err)
	}
	jobStore, err := jobsqlitestore.Open(t.Context(), jobsqlitestore.Config{DB: fx.db})
	if err != nil {
		t.Fatalf("open job store: %v", err)
	}
	detail, err := json.Marshal(episodeDescriptor{OwnerID: owner, EpisodeID: episodeID})
	if err != nil {
		t.Fatalf("encode descriptor: %v", err)
	}
	running := job.Record{
		ID: "job-interrupted-editorial", Kind: kindEditorial, Status: job.StatusRunning,
		Attempt: 1, RootID: "job-interrupted-editorial",
		Progress:  job.Progress{Stage: "start", Detail: detail},
		UpdatedAt: time.Now(),
	}
	if err := jobStore.Create(t.Context(), running); err != nil {
		t.Fatalf("create running job: %v", err)
	}
	unfinished, err := jobStore.Unfinished(t.Context())
	if err != nil {
		t.Fatalf("list unfinished: %v", err)
	}
	events := stream.New(stream.Config{})
	resolver := &render.Resolver{DB: fx.db.Writer(), WorkDir: t.TempDir(), Locate: fx.pipe.locateStems}
	rec := openTestReconciler(t, fx, fakeDiary{db: fx.db}, fakeLeaseSettler{})
	sweeper, err := broker.NewSweeper(broker.SweeperConfig{
		DB: fx.db, Source: stubSource{}, Statuses: fakeStatuses{},
		Ender: fakeEnder{}, Reconciler: rec,
	})
	if err != nil {
		t.Fatalf("open sweeper: %v", err)
	}
	runner, err := job.Open(t.Context(), job.Config{
		Broker: events, Store: jobStore, Kinds: fx.pipe.kinds(resolver, rec, sweeper),
	})
	if err != nil {
		t.Fatalf("open runner: %v", err)
	}
	_ = runner
	failInterruptedEpisodes(t.Context(), fx.db, jobStore, unfinished)
	done, err := jobStore.Get(t.Context(), running.ID)
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if done.Status != job.StatusInterrupted {
		t.Fatalf("job status = %q, want interrupted", done.Status)
	}
	var state string
	if err := fx.db.Reader().QueryRowContext(t.Context(),
		`SELECT state FROM episodes WHERE id = ?`, episodeID).Scan(&state); err != nil {
		t.Fatalf("read episode state: %v", err)
	}
	if state != string(episode.StateFailed) {
		t.Fatalf("episode state = %q, want failed", state)
	}
}

// TestPipelineFactoriesConstructJobs proves every pipeline pass builds
// its job func from the shared wiring, with the episode linkage the
// restart pass reads. It drives the job budget adapter directly and
// proves the render locator fails before touching media.
func TestPipelineFactoriesConstructJobs(t *testing.T) {
	fx := openWireFixture(t)
	const owner = "owner-factories"
	insertWireUser(t, fx, owner)
	const episodeID = "episode-factories"
	funcs := map[string]job.Func{
		kindEditTranscript: fx.pipe.editTranscriptFunc(owner, episodeID, []byte{0x4f, 0x67}, 60, nil, transcript.Offsets{}),
		kindEditorial:      fx.pipe.editorialFunc(owner, episodeID, []byte{0x4f}, []byte{0x67}, 60),
		kindAnalysis:       fx.pipe.analysisFunc(owner, episodeID),
		kindCover:          fx.pipe.coverFunc(owner, episodeID),
		kindMemory:         fx.pipe.memoryFunc(owner, episodeID),
	}
	if len(funcs) != 5 {
		t.Fatalf("funcs = %d, want one per paid pipeline kind", len(funcs))
	}
	for name, fn := range funcs {
		if fn == nil {
			t.Fatalf("kind %q built no job func", name)
		}
	}
	budgets := fx.pipe.budgetsFor(owner)
	if err := budgets.Reserve(cost.Price(1000)); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if err := budgets.Settle(cost.Price(1000), cost.Price(400)); err != nil {
		t.Fatalf("settle: %v", err)
	}
	if got := wireSpent(t, fx, owner); got != cost.Price(400) {
		t.Fatalf("spent = %s, want the settled 400", got)
	}
	freed := fx.pipe.budgetsFor(owner)
	if err := freed.Reserve(cost.Price(100)); err != nil {
		t.Fatalf("reserve: %v", err)
	}
	freed.Release(cost.Price(100))
	if got := wireHeld(t, fx); got != 0 {
		t.Fatalf("held = %s, want no hold after release", got)
	}
	if _, err := fx.pipe.locateRender(t.Context(), owner, "no-such-episode"); err == nil {
		t.Fatal("locate render succeeded with no render rows")
	}
}

// TestUploadOpenResolvesSessionOwnerAndRefusalCarriesHeader opens an
// upload with the placeholder owner, completes it, and reads it back.
// The owner reads its own bytes, while a cookieless read answers 404
// with private no-store on both the stored blob and an unknown id.
func TestUploadOpenResolvesSessionOwnerAndRefusalCarriesHeader(t *testing.T) {
	ctx := t.Context()
	db, err := keelsqlite.Open(ctx, keelsqlite.Config{Path: filepath.Join(t.TempDir(), "upload-owner.db")})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := reprisestore.Open(ctx, db); err != nil {
		t.Fatalf("migrate diary schema: %v", err)
	}
	identitySvc, err := identity.New(ctx, identity.Config{DB: db, SigningKey: "upload-owner-test-signing-key"})
	if err != nil {
		t.Fatalf("open identity: %v", err)
	}
	mediaDir := t.TempDir()
	mediaIndex, err := mediasqlitestore.Open(ctx, mediasqlitestore.Config{DB: db})
	if err != nil {
		t.Fatalf("open media index: %v", err)
	}
	media, err := mediastore.Open(ctx, mediastore.Config{
		Dir:          mediaDir,
		Index:        mediaIndex,
		ContentTypes: mediaContentTypes,
		Authorize:    identitySvc.AuthorizeMedia,
	})
	if err != nil {
		t.Fatalf("open media store: %v", err)
	}
	uploads, err := upload.New(upload.Config{
		Dir:      filepath.Join(t.TempDir(), "stage"),
		Store:    media,
		BasePath: api.UploadBasePath,
	})
	if err != nil {
		t.Fatalf("open upload handler: %v", err)
	}
	t.Cleanup(func() { _ = uploads.Close() })

	mux := http.NewServeMux()
	mux.Handle(api.UploadBasePath, identitySvc.Middleware(withUploadOwner(uploads)))
	mux.Handle(api.UploadBasePath+"/", identitySvc.Middleware(withUploadOwner(uploads)))
	mux.Handle("GET /media/{id}", identitySvc.Middleware(withMediaRefusalHeader(media)))

	openReq := httptest.NewRequest(http.MethodPost, api.UploadBasePath,
		bytes.NewReader([]byte(`{"owner":"guest","content_type":"audio/ogg","visibility":"private"}`)))
	openRec := httptest.NewRecorder()
	mux.ServeHTTP(openRec, openReq)
	if openRec.Code != http.StatusCreated {
		t.Fatalf("open status = %d, want 201: %s", openRec.Code, openRec.Body.String())
	}
	var opened struct {
		ID    string `json:"id"`
		Owner string `json:"owner"`
	}
	if err := json.Unmarshal(openRec.Body.Bytes(), &opened); err != nil {
		t.Fatalf("decode open response: %v", err)
	}
	if opened.Owner == "" || opened.Owner == "guest" {
		t.Fatalf("open owner = %q, want the session user id", opened.Owner)
	}
	var cookie *http.Cookie
	for _, c := range openRec.Result().Cookies() {
		if c.Name == identity.CookieName {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("open set no session cookie")
	}

	payload := []byte("owner stem bytes for the reload pin")
	sum := sha256.Sum256(payload)
	digest := hex.EncodeToString(sum[:])
	putReq := httptest.NewRequest(http.MethodPut, api.UploadBasePath+"/"+opened.ID+"/chunks/0", bytes.NewReader(payload))
	putReq.Header.Set("X-Chunk-SHA256", digest)
	putReq.AddCookie(cookie)
	putRec := httptest.NewRecorder()
	mux.ServeHTTP(putRec, putReq)
	if putRec.Code != http.StatusOK {
		t.Fatalf("chunk status = %d, want 200: %s", putRec.Code, putRec.Body.String())
	}
	completeReq := httptest.NewRequest(http.MethodPost, api.UploadBasePath+"/"+opened.ID+"/complete",
		bytes.NewReader([]byte(`{"sha256":"`+digest+`"}`)))
	completeReq.AddCookie(cookie)
	completeRec := httptest.NewRecorder()
	mux.ServeHTTP(completeRec, completeReq)
	if completeRec.Code != http.StatusCreated {
		t.Fatalf("complete status = %d, want 201: %s", completeRec.Code, completeRec.Body.String())
	}

	ownReq := httptest.NewRequest(http.MethodGet, "/media/"+opened.ID, nil)
	ownReq.AddCookie(cookie)
	ownRec := httptest.NewRecorder()
	mux.ServeHTTP(ownRec, ownReq)
	if ownRec.Code != http.StatusOK {
		t.Fatalf("owner media status = %d, want 200: %s", ownRec.Code, ownRec.Body.String())
	}
	if !bytes.Equal(ownRec.Body.Bytes(), payload) {
		t.Fatalf("owner media body = %q, want exactly the persisted bytes", ownRec.Body.String())
	}
	if got := ownRec.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Fatalf("owner media Cache-Control = %q, want private no-store", got)
	}

	anonReq := httptest.NewRequest(http.MethodGet, "/media/"+opened.ID, nil)
	anonRec := httptest.NewRecorder()
	mux.ServeHTTP(anonRec, anonReq)
	if anonRec.Code != http.StatusNotFound {
		t.Fatalf("anon media status = %d, want 404", anonRec.Code)
	}
	if got := anonRec.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Fatalf("anon refusal Cache-Control = %q, want private no-store", got)
	}

	unknown, err := keelid.New()
	if err != nil {
		t.Fatalf("mint unknown id: %v", err)
	}
	missingReq := httptest.NewRequest(http.MethodGet, "/media/"+unknown, nil)
	missingRec := httptest.NewRecorder()
	mux.ServeHTTP(missingRec, missingReq)
	if missingRec.Code != http.StatusNotFound {
		t.Fatalf("unknown media status = %d, want 404", missingRec.Code)
	}
	if got := missingRec.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Fatalf("unknown refusal Cache-Control = %q, want private no-store", got)
	}
}

// seedRenderedWords stores one rendered word row the marking pass
// reads. The quote sits in the words, so the scripted empty marking
// still exercises the full verify and store path.
func seedRenderedWords(t *testing.T, fx *wireFixture, owner, episodeID string) {
	t.Helper()
	if _, err := fx.db.Writer().ExecContext(t.Context(),
		`INSERT INTO words (id, owner_id, episode_id, text, start_ms, end_ms, source)
		VALUES ('word-cover-1', ?, ?, 'harbor', 0, 500, 'rendered')`,
		owner, episodeID); err != nil {
		t.Fatalf("seed words: %v", err)
	}
}

// TestPipelineCoverAndMemoryRunOffline runs the cover and marking
// passes end to end against the scripted model with real stores. It
// proves the shared client, the settings model id, and the durable
// budget settle through the real pass code with no network.
func TestPipelineCoverAndMemoryRunOffline(t *testing.T) {
	fx := openWireFixture(t)
	const owner = "owner-offline-runs"
	insertWireUser(t, fx, owner)
	diary, err := broker.NewSQLiteDiary(fx.db)
	if err != nil {
		t.Fatalf("open diary: %v", err)
	}
	episodeID, _, err := diary.CreateEpisodeAndSession(t.Context(), owner, 1800)
	if err != nil {
		t.Fatalf("create episode: %v", err)
	}
	seedRenderedWords(t, fx, owner, episodeID)
	quiet := func(job.Progress) {}
	coverRaw, err := fx.pipe.coverFunc(owner, episodeID)(t.Context(), quiet)
	if err != nil {
		t.Fatalf("cover run: %v", err)
	}
	var coverRes cover.Result
	if err := json.Unmarshal(coverRaw, &coverRes); err != nil {
		t.Fatalf("decode cover result: %v", err)
	}
	if coverRes.File == "" {
		t.Fatalf("cover result %+v names no file", coverRes)
	}
	memoryRaw, err := fx.pipe.memoryFunc(owner, episodeID)(t.Context(), quiet)
	if err != nil {
		t.Fatalf("memory run: %v", err)
	}
	var memoryRes memory.MarkResult
	if err := json.Unmarshal(memoryRaw, &memoryRes); err != nil {
		t.Fatalf("decode memory result: %v", err)
	}
	if got := wireSpent(t, fx, owner); got <= 0 {
		t.Fatalf("spent = %s, want both passes booked", got)
	}
}

// takeGateRoute pairs one take route name with the burst the binary wires.
type takeGateRoute struct {
	name  string
	burst int
}

// takeGateRoutes lists every take route with its wired burst, so the pins
// below run the production numbers instead of copies.
func takeGateRoutes() []takeGateRoute {
	return []takeGateRoute{
		{"take-sessions", takeSessionsBurst},
		{"take-session-end", takeSessionEndBurst},
		{"take-episodes", takeEpisodesBurst},
		{"take-threads", takeThreadsBurst},
		{"take-admin", takeAdminBurst},
		{"take-uploads", takeUploadsBurst},
		{"take-media", takeMediaBurst},
		{"take-stems", takeStemsBurst},
	}
}

// takeGateHandlers wraps one stub per take route in the wired budgets on
// one shared gate, the way the binary wires them.
func takeGateHandlers(t *testing.T) map[string]http.Handler {
	t.Helper()
	spendGate, err := gate.New(gate.Config{})
	if err != nil {
		t.Fatalf("open spend gate: %v", err)
	}
	handlers := map[string]http.Handler{}
	for _, route := range takeGateRoutes() {
		protected, err := protectTake(spendGate, route.name, route.burst, stubHandler())
		if err != nil {
			t.Fatalf("protect %s: %v", route.name, err)
		}
		handlers[route.name] = protected
	}
	return handlers
}

// takeGateHit sends one take request from one client and returns the answer.
func takeGateHit(handler http.Handler) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/take", nil)
	req.RemoteAddr = "198.51.100.7:4321"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

// requireRateLimitHints requires an honest refusal: a 429 with the shared
// code, a positive wait in the header, and the same wait in the body.
func requireRateLimitHints(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("refusal status = %d, want 429: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "rate_limited") {
		t.Fatalf("refusal body = %s, want the rate code", rec.Body.String())
	}
	header, err := strconv.Atoi(rec.Header().Get("Retry-After"))
	if err != nil || header < 1 {
		t.Fatalf("Retry-After = %q, want a positive wait", rec.Header().Get("Retry-After"))
	}
	var body struct {
		Error struct {
			Detail struct {
				RetryAfterSeconds int `json:"retry_after_seconds"`
			} `json:"detail"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode refusal: %v", err)
	}
	if body.Error.Detail.RetryAfterSeconds != header {
		t.Fatalf("body wait = %d, header wait = %d, want them to agree",
			body.Error.Detail.RetryAfterSeconds, header)
	}
}

// TestTakeGateHonestTakePasses replays one honest take with two stems at
// real cadence through the wired budgets and requires zero refusals.
func TestTakeGateHonestTakePasses(t *testing.T) {
	handlers := takeGateHandlers(t)
	cadence := map[string]int{
		"take-sessions":    1,
		"take-uploads":     30,
		"take-stems":       2,
		"take-session-end": 1,
		"take-episodes":    6,
		"take-media":       4,
		"take-threads":     2,
		"take-admin":       1,
	}
	for _, route := range takeGateRoutes() {
		for i := range cadence[route.name] {
			if rec := takeGateHit(handlers[route.name]); rec.Code != http.StatusOK {
				t.Fatalf("%s hit %d status = %d, want 200: %s", route.name, i, rec.Code, rec.Body.String())
			}
		}
	}
}

// TestTakeGateDoubleCadenceTrips replays the honest take twice as fast
// and requires the hammered upload route to trip with honest hints.
func TestTakeGateDoubleCadenceTrips(t *testing.T) {
	handlers := takeGateHandlers(t)
	cadence := map[string]int{
		"take-sessions":    2,
		"take-uploads":     60,
		"take-stems":       4,
		"take-session-end": 2,
		"take-episodes":    12,
		"take-media":       8,
		"take-threads":     4,
		"take-admin":       2,
	}
	refused := map[string]int{}
	for _, route := range takeGateRoutes() {
		for i := range cadence[route.name] {
			rec := takeGateHit(handlers[route.name])
			switch rec.Code {
			case http.StatusOK:
			case http.StatusTooManyRequests:
				refused[route.name]++
				requireRateLimitHints(t, rec)
			default:
				t.Fatalf("%s hit %d status = %d, want 200 or 429: %s", route.name, i, rec.Code, rec.Body.String())
			}
		}
	}
	if refused["take-uploads"] != 12 {
		t.Fatalf("upload refusals = %d, want 12 past the 48 burst", refused["take-uploads"])
	}
	for _, route := range takeGateRoutes() {
		if route.name == "take-uploads" {
			continue
		}
		if refused[route.name] != 0 {
			t.Fatalf("%s refusals = %d, want 0 at double cadence", route.name, refused[route.name])
		}
	}
}

// TestTakeGateRouteBurstsTripAtWiredEdge pins the exact burst each take
// route wires: the burst passes and the next request trips with hints.
func TestTakeGateRouteBurstsTripAtWiredEdge(t *testing.T) {
	edges := map[string]int{
		"take-uploads":  48,
		"take-stems":    8,
		"take-sessions": 6,
	}
	for _, route := range takeGateRoutes() {
		edge, ok := edges[route.name]
		if !ok {
			continue
		}
		spendGate, err := gate.New(gate.Config{})
		if err != nil {
			t.Fatalf("open spend gate: %v", err)
		}
		protected, err := protectTake(spendGate, route.name, route.burst, stubHandler())
		if err != nil {
			t.Fatalf("protect %s: %v", route.name, err)
		}
		for i := range edge {
			if rec := takeGateHit(protected); rec.Code != http.StatusOK {
				t.Fatalf("%s hit %d status = %d, want 200: %s", route.name, i, rec.Code, rec.Body.String())
			}
		}
		requireRateLimitHints(t, takeGateHit(protected))
	}
}

// TestTakeGateUploadHammerLeavesMint hammers uploads past its burst from
// one client and requires a session mint to still pass.
func TestTakeGateUploadHammerLeavesMint(t *testing.T) {
	handlers := takeGateHandlers(t)
	for range 60 {
		takeGateHit(handlers["take-uploads"])
	}
	if rec := takeGateHit(handlers["take-sessions"]); rec.Code != http.StatusOK {
		t.Fatalf("mint after upload hammer status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
}

// TestRetriedCompletionGarbageIDsRecoverToDraft posts unknown blob ids
// for an episode whose first attempt already linked its pair. The retry
// recovers to draft with a scheduled pass instead of stalling.
func TestRetriedCompletionGarbageIDsRecoverToDraft(t *testing.T) {
	fx := openWireFixture(t)
	handler, _, cookie, episodeID, _, _, _ := completionFixture(t, fx)
	code, answer := postCompletion(t, handler, cookie, episodeID, "garbage-user-orphan", "garbage-host-orphan")
	if code != http.StatusOK {
		t.Fatalf("retry status = %d, want 200", code)
	}
	if answer.State != string(episode.StateDraft) {
		t.Fatalf("retry state = %q, want draft", answer.State)
	}
	if !answer.Scheduled || answer.JobID == "" {
		t.Fatalf("retry = %+v, want a scheduled pass", answer)
	}
}

// TestCompletionUnknownIDsNameMissing posts unknown blob ids for an
// episode with nothing linked and requires a 404 naming both ids.
func TestCompletionUnknownIDsNameMissing(t *testing.T) {
	fx := openWireFixture(t)
	sessionBroker := openWireBroker(t, fx)
	identitySvc, err := identity.New(t.Context(), identity.Config{DB: fx.db, SigningKey: "wire-test-signing-key"})
	if err != nil {
		t.Fatalf("open identity: %v", err)
	}
	session, cookie := mintWireSession(t, fx, sessionBroker)
	if cookie == nil {
		t.Fatal("mint set no guest cookie")
	}
	var owner string
	if err := fx.db.Reader().QueryRowContext(t.Context(),
		`SELECT owner_id FROM sessions WHERE id = ?`, session.SessionID).Scan(&owner); err != nil {
		t.Fatalf("read session owner: %v", err)
	}
	diary, err := broker.NewSQLiteDiary(fx.db)
	if err != nil {
		t.Fatalf("open diary: %v", err)
	}
	episodeID, _, err := diary.CreateEpisodeAndSession(t.Context(), owner, 1800)
	if err != nil {
		t.Fatalf("create episode: %v", err)
	}
	episodeSvc, err := episode.NewService(episode.Config{DB: fx.db, TranscriptKind: kindEditTranscript})
	if err != nil {
		t.Fatalf("new episode service: %v", err)
	}
	drafts := openDraftJobs(t, fx)
	handler := identitySvc.Middleware(newStemsComplete(episodeSvc, drafts, fx.db))
	unknownUser, unknownHost := "missing-user-orphan", "missing-host-orphan"
	body, err := json.Marshal(stemsCompleteRequest{
		UserMediaID: unknownUser, HostMediaID: unknownHost,
		UserSampleRate: 48000, HostSampleRate: 48000,
	})
	if err != nil {
		t.Fatalf("encode completion: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/episodes/"+episodeID+"/stems/complete", bytes.NewReader(body))
	req.AddCookie(cookie)
	req.SetPathValue("id", episodeID)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", rec.Code, rec.Body.String())
	}
	for _, want := range []string{"stems_not_found", "missing_media_ids", unknownUser, unknownHost} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("body = %s, want %q", rec.Body.String(), want)
		}
	}
}
