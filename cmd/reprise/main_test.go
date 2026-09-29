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
	"sync/atomic"
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
	"github.com/nrynss/reprise/internal/analysis"
	"github.com/nrynss/reprise/internal/api"
	"github.com/nrynss/reprise/internal/assemblyai"
	"github.com/nrynss/reprise/internal/broker"
	"github.com/nrynss/reprise/internal/cover"
	"github.com/nrynss/reprise/internal/episode"
	"github.com/nrynss/reprise/internal/gemini"
	"github.com/nrynss/reprise/internal/host"
	"github.com/nrynss/reprise/internal/identity"
	"github.com/nrynss/reprise/internal/memory"
	"github.com/nrynss/reprise/internal/privacy"
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

// TestHostBuilderMarksEachCallbackOnce seeds one owner with two unused
// callbacks on two episodes. Two mints greet with different callbacks and
// leave both rows used, so the host never repeats an opening.
func TestHostBuilderMarksEachCallbackOnce(t *testing.T) {
	ctx := context.Background()
	db, err := keelsqlite.Open(ctx, keelsqlite.Config{Path: filepath.Join(t.TempDir(), "test.db")})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	if _, err := reprisestore.Open(ctx, db); err != nil {
		t.Fatalf("migrate diary schema: %v", err)
	}
	writer := db.Writer()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := writer.ExecContext(ctx, query, args...); err != nil {
			t.Fatalf("exec %q: %v", query, err)
		}
	}
	exec("INSERT INTO users (id, kind, created_at, last_seen_at) VALUES ('owner-cb', 'guest', 1, 2)")
	exec("INSERT INTO episodes (id, owner_id, number, title, state, visibility, share_token, seeded) VALUES ('ep1', 'owner-cb', 1, 'Episode 1', 'ready', 'private', 'share-ep1', 0)")
	exec("INSERT INTO episodes (id, owner_id, number, title, state, visibility, share_token, seeded) VALUES ('ep2', 'owner-cb', 2, 'Episode 2', 'ready', 'private', 'share-ep2', 0)")
	exec("INSERT INTO mentions (id, owner_id, episode_id, kind, word_offset, quote) VALUES ('m1', 'owner-cb', 'ep1', 'topic', 3, 'the talk I keep dreading with my sister')")
	exec("INSERT INTO mentions (id, owner_id, episode_id, kind, word_offset, quote) VALUES ('m2', 'owner-cb', 'ep2', 'topic', 7, 'the allotment')")
	exec("INSERT INTO callbacks (id, owner_id, episode_id, mention_id, used) VALUES ('cb1', 'owner-cb', 'ep1', 'm1', 0)")
	exec("INSERT INTO callbacks (id, owner_id, episode_id, mention_id, used) VALUES ('cb2', 'owner-cb', 'ep2', 'm2', 0)")
	builder := hostBuilder{db: writer}
	first, err := builder.BuildSessionConfig(ctx, "owner-cb")
	if err != nil {
		t.Fatalf("first BuildSessionConfig error = %v, want nil", err)
	}
	second, err := builder.BuildSessionConfig(ctx, "owner-cb")
	if err != nil {
		t.Fatalf("second BuildSessionConfig error = %v, want nil", err)
	}
	if first.Greeting == second.Greeting {
		t.Fatalf("greetings match %q, want two different callbacks", first.Greeting)
	}
	for _, want := range []string{
		"the talk I keep dreading with my sister",
		"the allotment",
	} {
		if !strings.Contains(first.Greeting+second.Greeting, want) {
			t.Fatalf("greetings %q and %q miss %q", first.Greeting, second.Greeting, want)
		}
	}
	var used int
	if err := writer.QueryRowContext(ctx, "SELECT COUNT(*) FROM callbacks WHERE owner_id = 'owner-cb' AND used = 1").Scan(&used); err != nil {
		t.Fatalf("count used callbacks: %v", err)
	}
	if used != 2 {
		t.Fatalf("used callbacks = %d, want 2", used)
	}
}

// TestHostBuilderConcurrentMintsDiverge starts two same-owner mints behind
// one gate, so both load before either claims. The two greetings cite
// different callbacks and both rows read used, so a double start never
// repeats an opening.
func TestHostBuilderConcurrentMintsDiverge(t *testing.T) {
	ctx := context.Background()
	db, err := keelsqlite.Open(ctx, keelsqlite.Config{Path: filepath.Join(t.TempDir(), "test.db")})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	if _, err := reprisestore.Open(ctx, db); err != nil {
		t.Fatalf("migrate diary schema: %v", err)
	}
	writer := db.Writer()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := writer.ExecContext(ctx, query, args...); err != nil {
			t.Fatalf("exec %q: %v", query, err)
		}
	}
	exec("INSERT INTO users (id, kind, created_at, last_seen_at) VALUES ('owner-cb', 'guest', 1, 2)")
	exec("INSERT INTO episodes (id, owner_id, number, title, state, visibility, share_token, seeded) VALUES ('ep1', 'owner-cb', 1, 'Episode 1', 'ready', 'private', 'share-ep1', 0)")
	exec("INSERT INTO episodes (id, owner_id, number, title, state, visibility, share_token, seeded) VALUES ('ep2', 'owner-cb', 2, 'Episode 2', 'ready', 'private', 'share-ep2', 0)")
	exec("INSERT INTO mentions (id, owner_id, episode_id, kind, word_offset, quote) VALUES ('m1', 'owner-cb', 'ep1', 'topic', 3, 'the talk I keep dreading with my sister')")
	exec("INSERT INTO mentions (id, owner_id, episode_id, kind, word_offset, quote) VALUES ('m2', 'owner-cb', 'ep2', 'topic', 7, 'the allotment')")
	exec("INSERT INTO callbacks (id, owner_id, episode_id, mention_id, used) VALUES ('cb1', 'owner-cb', 'ep1', 'm1', 0)")
	exec("INSERT INTO callbacks (id, owner_id, episode_id, mention_id, used) VALUES ('cb2', 'owner-cb', 'ep2', 'm2', 0)")
	builder := hostBuilder{db: writer}
	start := make(chan struct{})
	var wg sync.WaitGroup
	greetings := make([]string, 2)
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			cfg, err := builder.BuildSessionConfig(ctx, "owner-cb")
			if err != nil {
				errs[i] = err
				return
			}
			greetings[i] = cfg.Greeting
		}(i)
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("mint %d error = %v, want nil", i, err)
		}
	}
	if greetings[0] == greetings[1] {
		t.Fatalf("concurrent greetings match %q, want two different callbacks", greetings[0])
	}
	var used int
	if err := writer.QueryRowContext(ctx, "SELECT COUNT(*) FROM callbacks WHERE owner_id = 'owner-cb' AND used = 1").Scan(&used); err != nil {
		t.Fatalf("count used callbacks: %v", err)
	}
	if used != 2 {
		t.Fatalf("used callbacks = %d, want 2", used)
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
	index   *mediasqlitestore.Store
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
	if err := memory.Migrate(ctx, db); err != nil {
		t.Fatalf("migrate memory schema: %v", err)
	}
	if err := analysis.Migrate(ctx, db); err != nil {
		t.Fatalf("migrate analysis schema: %v", err)
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
	pipe.coverModelID = "test-cover"
	return &wireFixture{db: db, costs: costs, budgets: budgets, media: media, index: mediaIndex, pipe: pipe, ceiling: ceiling}
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
		// The recorded close carries no links, so a wait only burns the
		// artifact backoff these tests never pin.
		Wait: func(context.Context, time.Duration) error { return nil },
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
	renderKind, err := (&jobs{resolver: resolver, renderConcurrency: 1}).renderKind()
	if err != nil {
		t.Fatalf("render kind: %v", err)
	}
	previewKind, err := (&jobs{resolver: resolver, renderConcurrency: 1}).previewKind()
	if err != nil {
		t.Fatalf("preview kind: %v", err)
	}
	kinds := fx.pipe.kinds(renderKind, previewKind, rec, sweeper)
	if len(kinds) != 9 {
		t.Fatalf("kinds = %d, want 9 pipeline and settle kinds", len(kinds))
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
	for _, name := range []string{kindRender, kindPreview, broker.KindName, broker.SweepKindName} {
		kind, ok := kinds[name]
		if !ok {
			t.Fatalf("kind %q is not registered", name)
		}
		if !kind.Idempotent || kind.Resume == nil {
			t.Fatalf("kind %q does not resume, want the idempotent path", name)
		}
	}
	if kinds[kindRender].MaxAttempts < 2 {
		t.Fatalf("render attempts = %d, want room for a resumed attempt", kinds[kindRender].MaxAttempts)
	}
	if kinds[kindPreview].MaxAttempts < 2 {
		t.Fatalf("preview attempts = %d, want room for a resumed attempt", kinds[kindPreview].MaxAttempts)
	}
	if paidKinds[kindPreview] {
		t.Fatal("preview kind is paid, want no budget reservation on a free mix")
	}
	if fx.pipe.editorialModel != "test-editorial" {
		t.Fatalf("editorial model = %q, want the settings value", fx.pipe.editorialModel)
	}
	if fx.pipe.coverModelID != "test-cover" {
		t.Fatalf("cover model = %q, want the cover settings value", fx.pipe.coverModelID)
	}
	if fx.pipe.coverModelID == fx.pipe.editorialModel {
		t.Fatal("cover model matches the editorial model, want the image model")
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

// settleProbeStarter counts the reconcile starts the settle wrapper
// requests. The started func never runs, so the test pins the start
// alone with no provider traffic.
type settleProbeStarter struct {
	calls int
	kinds []string
}

// StartKind records the call and answers a fixed id.
func (s *settleProbeStarter) StartKind(_ context.Context, kind string, _ job.Func) (string, error) {
	s.calls++
	s.kinds = append(s.kinds, kind)
	return "job-probe", nil
}

// TestProviderReportRecordsWithoutSettle posts the learned provider id to
// the provider route, then closes on the end route. The provider post
// answers 200 with no reconcile job. The end post starts exactly one.
func TestProviderReportRecordsWithoutSettle(t *testing.T) {
	fx := openWireFixture(t)
	sessionBroker := openWireBroker(t, fx)
	diary, err := broker.NewSQLiteDiary(fx.db)
	if err != nil {
		t.Fatalf("open diary: %v", err)
	}
	rec := openTestReconciler(t, fx, diary, sessionBroker.Leases())

	session, _ := mintWireSession(t, fx, sessionBroker)
	if _, err := fx.db.Writer().ExecContext(t.Context(),
		`UPDATE sessions SET provider_session_id = ? WHERE id = ?`, "prov-1", session.SessionID); err != nil {
		t.Fatalf("record provider close: %v", err)
	}
	starter := &settleProbeStarter{}
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	wrapped := &settleOnEnd{inner: inner, banks: sessionBroker, starter: starter, rec: rec}
	mux := http.NewServeMux()
	mux.Handle("POST /api/sessions/{id}/end", wrapped)
	mux.Handle("POST /api/sessions/{id}/provider", wrapped)
	post := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"provider_session_id":"prov-1"}`))
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, req)
		return recorder
	}

	if got := post("/api/sessions/" + session.SessionID + "/provider"); got.Code != http.StatusOK {
		t.Fatalf("provider status = %d, want 200", got.Code)
	}
	if starter.calls != 0 {
		t.Fatalf("starts after provider post = %d, want none", starter.calls)
	}
	if got := post("/api/sessions/" + session.SessionID + "/end"); got.Code != http.StatusOK {
		t.Fatalf("end status = %d, want 200", got.Code)
	}
	if starter.calls != 1 {
		t.Fatalf("starts after end post = %d, want exactly one", starter.calls)
	}
	if len(starter.kinds) != 1 || starter.kinds[0] != broker.KindName {
		t.Fatalf("started kinds = %v, want one reconcile", starter.kinds)
	}
}

// TestRestartFailsInterruptedPaidJobs leaves a transcript job running,
// then boots the job wiring the way a restart does. Recovery marks the
// job interrupted, and the restart pass fails its episode for an explicit
// retry instead of rerunning the paid call. No word timeline landed, so
// the episode has nothing to ship.
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
	plantRunningJob(t, fx, "job-interrupted-transcript", kindEditTranscript,
		episodeDescriptor{OwnerID: owner, EpisodeID: episodeID})
	j := bootFinishJobs(t, fx)
	done, err := j.store.Get(t.Context(), "job-interrupted-transcript")
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if done.Status != job.StatusInterrupted {
		t.Fatalf("job status = %q, want interrupted", done.Status)
	}
	if got := finishState(t, fx, episodeID); got != episode.StateFailed {
		t.Fatalf("episode state = %q, want failed", got)
	}
	if n := kindJobsFor(t, fx, kindEditTranscript, episodeID); n != 1 {
		t.Fatalf("transcript jobs = %d, want the interrupted one alone", n)
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
		kindAnalysis:       fx.pipe.analysisFunc(owner, episodeID, "render-factories"),
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
	if _, err := fx.pipe.locateRender(t.Context(), owner, "no-such-episode", "no-such-render"); err == nil {
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

// imageModelProbe answers one image call with bytes the validator
// rejects and records the model id the call named, so the test reads
// which settings model the cover pass ran with.
func imageModelProbe(got *string) *gemini.Client {
	return gemini.NewTestClient(func(_ context.Context, model string, contents []*genai.Content, config *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
		for _, modality := range config.ResponseModalities {
			if modality == "IMAGE" {
				*got = model
				return &genai.GenerateContentResponse{
					Candidates: []*genai.Candidate{{
						Content: &genai.Content{Role: "model", Parts: []*genai.Part{{
							InlineData: &genai.Blob{MIMEType: "image/png", Data: []byte{0x89, 0x50, 0x4e, 0x47}},
						}}},
					}},
				}, nil
			}
		}
		return &genai.GenerateContentResponse{
			Candidates: []*genai.Candidate{{
				Content:      &genai.Content{Role: "model", Parts: []*genai.Part{{Text: `{"title":"Harbor Light"}`}}},
				FinishReason: genai.FinishReasonStop,
			}},
		}, nil
	})
}

// TestCoverFuncReceivesCoverModel runs the cover pass against a client
// that records the model id. The call must name the cover settings
// model, never the editorial one. Pointing the pass back at the
// editorial model fails here.
func TestCoverFuncReceivesCoverModel(t *testing.T) {
	fx := openWireFixture(t)
	const owner = "owner-cover-model"
	insertWireUser(t, fx, owner)
	diary, err := broker.NewSQLiteDiary(fx.db)
	if err != nil {
		t.Fatalf("open diary: %v", err)
	}
	episodeID, _, err := diary.CreateEpisodeAndSession(t.Context(), owner, 1800)
	if err != nil {
		t.Fatalf("create episode: %v", err)
	}
	var imageModel string
	fx.pipe.coverModel = geminiCover{client: imageModelProbe(&imageModel)}
	quiet := func(job.Progress) {}
	if _, err := fx.pipe.coverFunc(owner, episodeID)(t.Context(), quiet); err != nil {
		t.Fatalf("cover run: %v", err)
	}
	if imageModel != "test-cover" {
		t.Fatalf("cover model = %q, want the cover settings value", imageModel)
	}
	if imageModel == fx.pipe.editorialModel {
		t.Fatal("cover call named the editorial model, want the image model")
	}
}

// TestPlainCoverUsesCoverModel poisons the editorial model id and runs
// the deterministic cover fallback. The run still stores its panel,
// which proves the fallback names the cover settings model instead of
// the editorial one.
func TestPlainCoverUsesCoverModel(t *testing.T) {
	fx := openWireFixture(t)
	const owner = "owner-plain-cover-model"
	insertWireUser(t, fx, owner)
	diary, err := broker.NewSQLiteDiary(fx.db)
	if err != nil {
		t.Fatalf("open diary: %v", err)
	}
	episodeID, _, err := diary.CreateEpisodeAndSession(t.Context(), owner, 1800)
	if err != nil {
		t.Fatalf("create episode: %v", err)
	}
	fx.pipe.coverModelID = "test-cover"
	fx.pipe.editorialModel = ""
	j := &jobs{pipe: fx.pipe}
	if err := j.plainCover(t.Context(), owner, episodeID); err != nil {
		t.Fatalf("plain cover: %v", err)
	}
	var fallback int
	if err := fx.db.Reader().QueryRowContext(t.Context(),
		`SELECT fallback FROM covers WHERE episode_id = ?`, episodeID).Scan(&fallback); err != nil {
		t.Fatalf("read cover row: %v", err)
	}
	if fallback != 1 {
		t.Fatalf("cover fallback = %d, want the deterministic panel", fallback)
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

// TestTakeRuleRefillsBurstPerMinute requires the wired episodes rule to
// refill its whole burst once a minute: one token every 500 ms per
// client, with the global bucket on its own sixteenfold cadence.
// Uploads keep the per token refill. Setting Every back to the whole
// minute fails this test.
func TestTakeRuleRefillsBurstPerMinute(t *testing.T) {
	rule := takeRule("take-episodes", 120)
	if rule.PerClient.Burst != 120 {
		t.Fatalf("PerClient burst = %d, want 120", rule.PerClient.Burst)
	}
	if rule.PerClient.Every != 500*time.Millisecond {
		t.Fatalf("PerClient Every = %v, want 500ms for a 120 burst per minute", rule.PerClient.Every)
	}
	wantGlobal := time.Minute / time.Duration(16*120)
	if rule.Global.Burst != 16*120 {
		t.Fatalf("Global burst = %d, want %d", rule.Global.Burst, 16*120)
	}
	if rule.Global.Every != wantGlobal {
		t.Fatalf("Global Every = %v, want %v for a %d burst per minute", rule.Global.Every, wantGlobal, 16*120)
	}
	uploads := takeRule("take-uploads", takeUploadsBurst)
	if uploads.PerClient.Every != uploadRefill || uploads.Global.Every != uploadRefill {
		t.Fatalf("uploads Every = %v and %v, want the per token %v",
			uploads.PerClient.Every, uploads.Global.Every, uploadRefill)
	}
}

// TestTakeGateRefillsFullBurstAfterOneMinute drains the wired episodes
// burst on the gate clock seam, then advances one minute and passes the
// whole burst again before tripping.
func TestTakeGateRefillsFullBurstAfterOneMinute(t *testing.T) {
	start := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	now := start
	spendGate, err := gate.New(gate.Config{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("open spend gate: %v", err)
	}
	protected, err := protectTake(spendGate, "take-episodes", takeEpisodesBurst, stubHandler())
	if err != nil {
		t.Fatalf("protect take-episodes: %v", err)
	}
	for i := range takeEpisodesBurst {
		if rec := takeGateHit(protected); rec.Code != http.StatusOK {
			t.Fatalf("drain hit %d status = %d, want 200: %s", i, rec.Code, rec.Body.String())
		}
	}
	requireRateLimitHints(t, takeGateHit(protected))
	now = now.Add(time.Minute)
	for i := range takeEpisodesBurst {
		if rec := takeGateHit(protected); rec.Code != http.StatusOK {
			t.Fatalf("refilled hit %d status = %d, want 200: %s", i, rec.Code, rec.Body.String())
		}
	}
	requireRateLimitHints(t, takeGateHit(protected))
}

// stackTakeGate wraps the route in the outer budget and then its own
// budget, which is the order one chunk meets.
func stackTakeGate(t *testing.T, now func() time.Time, name string, burst int) http.Handler {
	t.Helper()
	spendGate, err := gate.New(gate.Config{Now: now})
	if err != nil {
		t.Fatalf("open spend gate: %v", err)
	}
	inner, err := protectTake(spendGate, name, burst, stubHandler())
	if err != nil {
		t.Fatalf("protect %s: %v", name, err)
	}
	handler, err := spendGate.Protect(outerAPIRule(), inner)
	if err != nil {
		t.Fatalf("protect outer: %v", err)
	}
	return handler
}

// TestTakeGateSustainedUploadClearsOuterBurst drives the outer budget
// and then the upload budget, which is the order one chunk meets. The
// clock steps by the spacing of two 48 kHz stems. The run is longer
// than the old outer burst and the shared outer burst. Zero requests
// are refused. An instant double then refuses twelve, with the rate
// limit body and a matching wait.
func TestTakeGateSustainedUploadClearsOuterBurst(t *testing.T) {
	start := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	now := start
	// 65536 byte chunks. Two 48 kHz 16-bit stems are 192000 bytes a second.
	chunkEvery := 65536 * time.Second / (2 * 96000)
	handler := stackTakeGate(t, func() time.Time { return now }, "take-uploads", takeUploadsBurst)
	// Past both outer bursts, including tokens a one minute refill
	// would restore while the clock runs.
	sustained := 16*outerAPIBurst + 128
	for i := range sustained {
		now = now.Add(chunkEvery)
		if rec := takeGateHit(handler); rec.Code != http.StatusOK {
			t.Fatalf("upload %d status = %d, want 200: %s", i, rec.Code, rec.Body.String())
		}
	}
	now = now.Add(uploadRefill)
	const hammer = 60
	refused := 0
	for i := range hammer {
		rec := takeGateHit(handler)
		switch rec.Code {
		case http.StatusOK:
		case http.StatusTooManyRequests:
			refused++
			requireRateLimitHints(t, rec)
			if rec.Header().Get("Retry-After") != "1" {
				t.Fatalf("Retry-After = %q, want 1", rec.Header().Get("Retry-After"))
			}
		default:
			t.Fatalf("upload hammer %d status = %d, want 200 or 429: %s", i, rec.Code, rec.Body.String())
		}
	}
	if refused != 12 {
		t.Fatalf("upload refusals = %d, want 12 past the 48 burst", refused)
	}
}

// TestTakeGateMintRefillStaysPerMinute sends session mints through the
// outer budget and then the mint budget. Six pass. The next refuses
// with a ten second wait. One second later it still refuses with nine,
// so the faster outer refill did not raise the mint rate.
func TestTakeGateMintRefillStaysPerMinute(t *testing.T) {
	start := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	now := start
	handler := stackTakeGate(t, func() time.Time { return now }, "take-sessions", takeSessionsBurst)
	for i := range takeSessionsBurst {
		if rec := takeGateHit(handler); rec.Code != http.StatusOK {
			t.Fatalf("mint %d status = %d, want 200: %s", i, rec.Code, rec.Body.String())
		}
	}
	rec := takeGateHit(handler)
	requireRateLimitHints(t, rec)
	if got := rec.Header().Get("Retry-After"); got != "10" {
		t.Fatalf("Retry-After = %q, want 10 for a ten second mint token", got)
	}
	now = now.Add(time.Second)
	rec = takeGateHit(handler)
	requireRateLimitHints(t, rec)
	if got := rec.Header().Get("Retry-After"); got != "9" {
		t.Fatalf("Retry-After = %q, want 9 one second later", got)
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

// scriptedTranscript answers one batch pass. The first fetch is the
// completed guest word. The fetch after delete is the deletion marker.
func scriptedTranscript() *httptest.Server {
	srv, _ := scriptedTranscriptUploads()
	return srv
}

// scriptedTranscriptUploads is scriptedTranscript with an upload count.
// One transcript run uploads the guest audio once.
func scriptedTranscriptUploads() (*httptest.Server, *atomic.Int32) {
	uploads := &atomic.Int32{}
	var mu sync.Mutex
	gets := 0
	completed := `{"id":"tx-host","status":"completed","text":"guest","audio_duration":1,` +
		`"words":[{"text":"guest","start":100,"end":250,"confidence":0.9,"speaker":null}]}`
	deleted := `{"id":"tx-host","status":"completed","text":"Deleted by user.",` +
		`"audio_url":"http://deleted_by_user","confidence":null,"words":null}`
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/upload", func(w http.ResponseWriter, r *http.Request) {
		uploads.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"upload_url":"http://`+r.Host+`/audio/host"}`)
	})
	mux.HandleFunc("/v2/transcript", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"tx-host","status":"queued"}`)
	})
	mux.HandleFunc("/v2/transcript/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusOK)
			return
		}
		mu.Lock()
		gets++
		body := deleted
		if gets == 1 {
			body = completed
		}
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	})
	return httptest.NewServer(mux), uploads
}

// waitWireJobs waits until id is done and no job is still running.
func waitWireJobs(t *testing.T, store job.Store, id string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		rec, err := store.Get(t.Context(), id)
		if err != nil {
			t.Fatalf("read job: %v", err)
		}
		switch rec.Status {
		case job.StatusDone:
		case job.StatusError, job.StatusCancelled, job.StatusInterrupted:
			t.Fatalf("job %s ended %s: %v", id, rec.Status, rec.Err)
		default:
			if time.Now().After(deadline) {
				t.Fatalf("job %s stayed %s", id, rec.Status)
			}
			time.Sleep(20 * time.Millisecond)
			continue
		}
		left, err := store.Unfinished(t.Context())
		if err != nil {
			t.Fatalf("list jobs: %v", err)
		}
		if len(left) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("jobs still running: %d", len(left))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestTranscriptStoresHostWordsAtReplyTimes runs one transcript for an
// episode whose timeline holds two host replies. The stored host words
// sit on those reply spans, shifted by the stem offsets. Passing no
// host replies leaves only the guest word, so this test fails.
func TestTranscriptStoresHostWordsAtReplyTimes(t *testing.T) {
	fx := openWireFixture(t)
	const owner = "owner-host-replies"
	insertWireUser(t, fx, owner)
	diary, err := broker.NewSQLiteDiary(fx.db)
	if err != nil {
		t.Fatalf("open diary: %v", err)
	}
	episodeID, sessionID, err := diary.CreateEpisodeAndSession(t.Context(), owner, 1800)
	if err != nil {
		t.Fatalf("create episode: %v", err)
	}
	body := sineWAV()
	userID := persistStemAudio(t, fx, owner, episodeID, body)
	hostID := persistStemAudio(t, fx, owner, episodeID, body)
	stems := []struct {
		role   string
		media  string
		offset int64
	}{
		{transcript.RoleUser, userID, 20},
		{transcript.RoleHost, hostID, 50},
	}
	for _, row := range stems {
		if _, err := fx.db.Writer().ExecContext(t.Context(),
			`INSERT INTO stems (id, owner_id, episode_id, media_id, role, sample_rate, start_offset_ms)
			 VALUES (?, ?, ?, ?, ?, 8000, ?)`,
			"stem-"+row.role, owner, episodeID, row.media, row.role, row.offset); err != nil {
			t.Fatalf("link %s stem: %v", row.role, err)
		}
	}
	const sessionStart int64 = 5_000_000
	timeline, err := json.Marshal(map[string]any{
		"started_at_unix_ms": sessionStart,
		"turns": []any{
			map[string]any{
				"agent_text":                "Alpha Beta",
				"agent_reply_started_at_ms": sessionStart + 1000,
				"agent_reply_ended_at_ms":   sessionStart + 1600,
			},
			map[string]any{
				"user_transcript": "not-host",
			},
			map[string]any{
				"agent_text":                "Gamma",
				"agent_reply_started_at_ms": sessionStart + 4000,
				"agent_reply_ended_at_ms":   sessionStart + 4800,
			},
		},
	})
	if err != nil {
		t.Fatalf("encode timeline: %v", err)
	}
	timelineID, err := fx.media.Persist(t.Context(), bytes.NewReader(timeline), mediastore.Put{
		ContentType: "application/json",
		Owner:       owner,
		Group:       episodeID,
		Visibility:  mediastore.Private,
	})
	if err != nil {
		t.Fatalf("persist timeline: %v", err)
	}
	if _, err := fx.db.Writer().ExecContext(t.Context(),
		`CREATE TABLE IF NOT EXISTS reconcile_state (
			session_id TEXT PRIMARY KEY,
			timeline_media_id TEXT NOT NULL DEFAULT ''
		)`); err != nil {
		t.Fatalf("create claim table: %v", err)
	}
	if _, err := fx.db.Writer().ExecContext(t.Context(),
		`INSERT INTO reconcile_state (session_id, timeline_media_id) VALUES (?, ?)`,
		sessionID, timelineID); err != nil {
		t.Fatalf("store timeline id: %v", err)
	}
	server := scriptedTranscript()
	t.Cleanup(server.Close)
	batch, err := assemblyai.NewBatchClient(assemblyai.Config{
		BaseURL: server.URL,
		APIKey:  "probe-key",
		Model:   "test-transcription",
		Client:  server.Client(),
	})
	if err != nil {
		t.Fatalf("open batch client: %v", err)
	}
	fx.pipe.batch = batch
	drafts := openDraftJobs(t, fx)
	jobID, started, err := drafts.startTranscript(t.Context(), owner, episodeID)
	if err != nil || !started || jobID == "" {
		t.Fatalf("start transcript = %q %v err %v, want a job", jobID, started, err)
	}
	waitWireJobs(t, drafts.store, jobID)
	stored, err := transcript.Load(t.Context(), fx.db.Writer(), episodeID)
	if err != nil {
		t.Fatalf("load words: %v", err)
	}
	// Guest 100 to 250 plus the user offset of 20.
	// Alpha and Beta run 1000 to 1600 on the host stem, then the host
	// offset of 50. Gamma runs 4000 to 4800, plus 50. Each host word
	// uses that whole reply span. The row stores no time per word.
	want := []transcript.Stored{
		{Text: "guest", StartMs: 120, EndMs: 270},
		{Text: "Alpha", StartMs: 1050, EndMs: 1650},
		{Text: "Beta", StartMs: 1050, EndMs: 1650},
		{Text: "Gamma", StartMs: 4050, EndMs: 4850},
	}
	if len(stored) != len(want) {
		t.Fatalf("stored = %+v, want %d words at the reply times", stored, len(want))
	}
	for i := range want {
		if stored[i].Text != want[i].Text || stored[i].StartMs != want[i].StartMs || stored[i].EndMs != want[i].EndMs {
			t.Fatalf("word %d = %+v, want %+v", i, stored[i], want[i])
		}
	}
}

// reconcileClaimTable is the claim table a live process creates at boot.
// Tests that wait on a timeline use this shape, not a missing table.
const reconcileClaimTable = `CREATE TABLE IF NOT EXISTS reconcile_state (
	session_id TEXT PRIMARY KEY,
	claimed INTEGER NOT NULL DEFAULT 0,
	settled INTEGER NOT NULL DEFAULT 0,
	connected_seconds INTEGER NOT NULL DEFAULT 0,
	cost_nd INTEGER NOT NULL DEFAULT 0,
	over_cap INTEGER NOT NULL DEFAULT 0,
	over_cap_alerted INTEGER NOT NULL DEFAULT 0,
	recording_media_id TEXT NOT NULL DEFAULT '',
	timeline_media_id TEXT NOT NULL DEFAULT '',
	recording_url TEXT NOT NULL DEFAULT '',
	timeline_url TEXT NOT NULL DEFAULT '',
	updated_at INTEGER NOT NULL DEFAULT 0
)`

// linkOffsetStems stores both stems and their alignment shifts.
func linkOffsetStems(t *testing.T, fx *wireFixture, owner, episodeID string, userOffset, hostOffset int64) {
	t.Helper()
	body := sineWAV()
	userID := persistStemAudio(t, fx, owner, episodeID, body)
	hostID := persistStemAudio(t, fx, owner, episodeID, body)
	rows := []struct {
		role   string
		media  string
		offset int64
	}{
		{transcript.RoleUser, userID, userOffset},
		{transcript.RoleHost, hostID, hostOffset},
	}
	for _, row := range rows {
		if _, err := fx.db.Writer().ExecContext(t.Context(),
			`INSERT INTO stems (id, owner_id, episode_id, media_id, role, sample_rate, start_offset_ms)
			 VALUES (?, ?, ?, ?, ?, 8000, ?)`,
			"stem-"+row.role, owner, episodeID, row.media, row.role, row.offset); err != nil {
			t.Fatalf("link %s stem: %v", row.role, err)
		}
	}
}

// twoReplyTimeline encodes two host replies and one guest turn.
func twoReplyTimeline(t *testing.T) []byte {
	t.Helper()
	const sessionStart int64 = 5_000_000
	raw, err := json.Marshal(map[string]any{
		"started_at_unix_ms": sessionStart,
		"turns": []any{
			map[string]any{
				"agent_text":                "Alpha Beta",
				"agent_reply_started_at_ms": sessionStart + 1000,
				"agent_reply_ended_at_ms":   sessionStart + 1600,
			},
			map[string]any{
				"user_transcript": "not-host",
			},
			map[string]any{
				"agent_text":                "Gamma",
				"agent_reply_started_at_ms": sessionStart + 4000,
				"agent_reply_ended_at_ms":   sessionStart + 4800,
			},
		},
	})
	if err != nil {
		t.Fatalf("encode timeline: %v", err)
	}
	return raw
}

// TestLateTimelineStillStoresHostWords starts the transcript while the
// claim row has no timeline id, then stores the two replies. The job
// waits and merges those words. It uploads the guest audio once.
func TestLateTimelineStillStoresHostWords(t *testing.T) {
	fx := openWireFixture(t)
	const owner = "owner-late-timeline"
	insertWireUser(t, fx, owner)
	diary, err := broker.NewSQLiteDiary(fx.db)
	if err != nil {
		t.Fatalf("open diary: %v", err)
	}
	episodeID, sessionID, err := diary.CreateEpisodeAndSession(t.Context(), owner, 1800)
	if err != nil {
		t.Fatalf("create episode: %v", err)
	}
	linkOffsetStems(t, fx, owner, episodeID, 20, 50)
	if _, err := fx.db.Writer().ExecContext(t.Context(), reconcileClaimTable); err != nil {
		t.Fatalf("create claim table: %v", err)
	}
	if _, err := fx.db.Writer().ExecContext(t.Context(),
		`INSERT INTO reconcile_state (session_id, settled, timeline_media_id, timeline_url)
		 VALUES (?, 0, '', '')`, sessionID); err != nil {
		t.Fatalf("store empty claim: %v", err)
	}
	server, uploads := scriptedTranscriptUploads()
	t.Cleanup(server.Close)
	batch, err := assemblyai.NewBatchClient(assemblyai.Config{
		BaseURL: server.URL,
		APIKey:  "probe-key",
		Model:   "test-transcription",
		Client:  server.Client(),
	})
	if err != nil {
		t.Fatalf("open batch client: %v", err)
	}
	fx.pipe.batch = batch
	drafts := openDraftJobs(t, fx)
	jobID, started, err := drafts.startTranscript(t.Context(), owner, episodeID)
	if err != nil || !started || jobID == "" {
		t.Fatalf("start transcript = %q %v err %v, want a job", jobID, started, err)
	}
	timelineID, err := fx.media.Persist(t.Context(), bytes.NewReader(twoReplyTimeline(t)), mediastore.Put{
		ContentType: "application/json",
		Owner:       owner,
		Group:       episodeID,
		Visibility:  mediastore.Private,
	})
	if err != nil {
		t.Fatalf("persist timeline: %v", err)
	}
	if _, err := fx.db.Writer().ExecContext(t.Context(),
		`UPDATE reconcile_state SET timeline_media_id = ? WHERE session_id = ?`,
		timelineID, sessionID); err != nil {
		t.Fatalf("store timeline id: %v", err)
	}
	waitWireJobs(t, drafts.store, jobID)
	if uploads.Load() != 1 {
		t.Fatalf("uploads = %d, want one batch", uploads.Load())
	}
	stored, err := transcript.Load(t.Context(), fx.db.Writer(), episodeID)
	if err != nil {
		t.Fatalf("load words: %v", err)
	}
	want := []transcript.Stored{
		{Text: "guest", StartMs: 120, EndMs: 270},
		{Text: "Alpha", StartMs: 1050, EndMs: 1650},
		{Text: "Beta", StartMs: 1050, EndMs: 1650},
		{Text: "Gamma", StartMs: 4050, EndMs: 4850},
	}
	if len(stored) != len(want) {
		t.Fatalf("stored = %+v, want guest, Alpha, Beta, and Gamma", stored)
	}
	for i := range want {
		if stored[i].Text != want[i].Text || stored[i].StartMs != want[i].StartMs || stored[i].EndMs != want[i].EndMs {
			t.Fatalf("word %d = %+v, want %+v", i, stored[i], want[i])
		}
	}
}

// TestSettledClaimWithoutTimelineStoresTheGuest runs the transcript
// after reconcile finished with no timeline. The job does not wait,
// and the stored words are the guest batch alone.
func TestSettledClaimWithoutTimelineStoresTheGuest(t *testing.T) {
	fx := openWireFixture(t)
	const owner = "owner-no-timeline"
	insertWireUser(t, fx, owner)
	diary, err := broker.NewSQLiteDiary(fx.db)
	if err != nil {
		t.Fatalf("open diary: %v", err)
	}
	episodeID, sessionID, err := diary.CreateEpisodeAndSession(t.Context(), owner, 1800)
	if err != nil {
		t.Fatalf("create episode: %v", err)
	}
	linkOffsetStems(t, fx, owner, episodeID, 20, 50)
	if _, err := fx.db.Writer().ExecContext(t.Context(), reconcileClaimTable); err != nil {
		t.Fatalf("create claim table: %v", err)
	}
	if _, err := fx.db.Writer().ExecContext(t.Context(),
		`INSERT INTO reconcile_state (session_id, settled, timeline_media_id, timeline_url)
		 VALUES (?, 1, '', '')`, sessionID); err != nil {
		t.Fatalf("store settled claim: %v", err)
	}
	server, uploads := scriptedTranscriptUploads()
	t.Cleanup(server.Close)
	batch, err := assemblyai.NewBatchClient(assemblyai.Config{
		BaseURL: server.URL,
		APIKey:  "probe-key",
		Model:   "test-transcription",
		Client:  server.Client(),
	})
	if err != nil {
		t.Fatalf("open batch client: %v", err)
	}
	fx.pipe.batch = batch
	drafts := openDraftJobs(t, fx)
	jobID, started, err := drafts.startTranscript(t.Context(), owner, episodeID)
	if err != nil || !started || jobID == "" {
		t.Fatalf("start transcript = %q %v err %v, want a job", jobID, started, err)
	}
	waitWireJobs(t, drafts.store, jobID)
	if uploads.Load() != 1 {
		t.Fatalf("uploads = %d, want one batch", uploads.Load())
	}
	stored, err := transcript.Load(t.Context(), fx.db.Writer(), episodeID)
	if err != nil {
		t.Fatalf("load words: %v", err)
	}
	if len(stored) != 1 || stored[0].Text != "guest" || stored[0].StartMs != 120 || stored[0].EndMs != 270 {
		t.Fatalf("stored = %+v, want only the guest word", stored)
	}
}

// TestHostWordBoundsComeFromTheStoredSpan reads one reply whose row
// stores the text and the span only. Each word bound is that start or
// that end. An interior slice is not a stored bound.
func TestHostWordBoundsComeFromTheStoredSpan(t *testing.T) {
	const sessionStart int64 = 5_000_000
	const replyStart int64 = sessionStart + 1000
	const replyEnd int64 = sessionStart + 1600
	raw, err := json.Marshal(map[string]any{
		"started_at_unix_ms": sessionStart,
		"turns": []any{
			map[string]any{
				"agent_text":                "Alpha Beta",
				"agent_reply_started_at_ms": replyStart,
				"agent_reply_ended_at_ms":   replyEnd,
			},
		},
	})
	if err != nil {
		t.Fatalf("encode timeline: %v", err)
	}
	replies, err := repliesFromTimeline(raw)
	if err != nil {
		t.Fatalf("replies: %v", err)
	}
	if len(replies) != 1 || len(replies[0].Words) != 2 {
		t.Fatalf("replies = %+v, want Alpha and Beta", replies)
	}
	span := replyEnd - replyStart
	allowed := map[int64]bool{0: true, span: true}
	for _, w := range replies[0].Words {
		if !allowed[w.StartMs] || !allowed[w.EndMs] {
			t.Fatalf("%s end %d is not a stored reply bound", w.Text, w.EndMs)
		}
	}
}

// mergedReplyWords is the guest word plus the two stored host replies,
// shifted by the stem offsets the timeline tests link.
func mergedReplyWords() []transcript.Stored {
	return []transcript.Stored{
		{Text: "guest", StartMs: 120, EndMs: 270},
		{Text: "Alpha", StartMs: 1050, EndMs: 1650},
		{Text: "Beta", StartMs: 1050, EndMs: 1650},
		{Text: "Gamma", StartMs: 4050, EndMs: 4850},
	}
}

// assertWords fails when the stored timeline does not match want.
func assertWords(t *testing.T, stored, want []transcript.Stored) {
	t.Helper()
	if len(stored) != len(want) {
		t.Fatalf("stored = %+v, want %d words", stored, len(want))
	}
	for i := range want {
		if stored[i].Text != want[i].Text || stored[i].StartMs != want[i].StartMs || stored[i].EndMs != want[i].EndMs {
			t.Fatalf("word %d = %+v, want %+v", i, stored[i], want[i])
		}
	}
}

// loadEditWords reads the stored edit timeline and fails the test on error.
func loadEditWords(t *testing.T, fx *wireFixture, episodeID string) []transcript.Stored {
	t.Helper()
	stored, err := transcript.Load(t.Context(), fx.db.Writer(), episodeID)
	if err != nil {
		t.Fatalf("load words: %v", err)
	}
	return stored
}

// newOffsetEpisode builds one episode with both stems, a scripted batch,
// and a draft runner. The stems carry the offsets the reply tests expect.
func newOffsetEpisode(t *testing.T, owner string) (*wireFixture, *jobs, string, string, *atomic.Int32) {
	t.Helper()
	fx := openWireFixture(t)
	insertWireUser(t, fx, owner)
	diary, err := broker.NewSQLiteDiary(fx.db)
	if err != nil {
		t.Fatalf("open diary: %v", err)
	}
	episodeID, sessionID, err := diary.CreateEpisodeAndSession(t.Context(), owner, 1800)
	if err != nil {
		t.Fatalf("create episode: %v", err)
	}
	linkOffsetStems(t, fx, owner, episodeID, 20, 50)
	server, uploads := scriptedTranscriptUploads()
	t.Cleanup(server.Close)
	batch, err := assemblyai.NewBatchClient(assemblyai.Config{
		BaseURL: server.URL,
		APIKey:  "probe-key",
		Model:   "test-transcription",
		Client:  server.Client(),
	})
	if err != nil {
		t.Fatalf("open batch client: %v", err)
	}
	fx.pipe.batch = batch
	return fx, openDraftJobs(t, fx), episodeID, sessionID, uploads
}

// insertClaim stores one reconcile claim for the session.
func insertClaim(t *testing.T, fx *wireFixture, sessionID string, settled int, mediaID string) {
	t.Helper()
	if _, err := fx.db.Writer().ExecContext(t.Context(), reconcileClaimTable); err != nil {
		t.Fatalf("create claim table: %v", err)
	}
	if _, err := fx.db.Writer().ExecContext(t.Context(),
		`INSERT INTO reconcile_state (session_id, settled, timeline_media_id, timeline_url)
		 VALUES (?, ?, ?, '')`, sessionID, settled, mediaID); err != nil {
		t.Fatalf("store claim: %v", err)
	}
}

// plantReconcile stores one reconcile job linked to the episode.
func plantReconcile(t *testing.T, drafts *jobs, id, owner, episodeID string, status job.Status) {
	t.Helper()
	detail, err := json.Marshal(episodeDescriptor{OwnerID: owner, EpisodeID: episodeID})
	if err != nil {
		t.Fatalf("encode reconcile linkage: %v", err)
	}
	stage := "read"
	rec := job.Record{
		ID: id, Kind: broker.KindName, Status: status,
		Attempt: 1, RootID: id,
		Progress:  job.Progress{Stage: stage, Detail: detail},
		UpdatedAt: time.Now(),
	}
	if status == job.StatusError {
		rec.Err = errors.New("reconcile ended")
		rec.Progress.Stage = "error"
	}
	if err := drafts.store.Create(t.Context(), rec); err != nil {
		t.Fatalf("plant reconcile: %v", err)
	}
}

// replaceTimelineBlob writes the finished timeline over a claimed id.
// The rename keeps a reader from observing a second prefix.
func replaceTimelineBlob(t *testing.T, fx *wireFixture, mediaID string, body []byte) {
	t.Helper()
	path := filepath.Join(fx.pipe.mediaDir, mediaID)
	tmp := path + ".next"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		t.Fatalf("write timeline: %v", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatalf("replace timeline: %v", err)
	}
}

// waitJobStatus waits until id reaches want. Another terminal status fails.
func waitJobStatus(t *testing.T, store job.Store, id string, want job.Status) job.Record {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		rec, err := store.Get(t.Context(), id)
		if err != nil {
			t.Fatalf("read job: %v", err)
		}
		if rec.Status == want {
			return rec
		}
		if jobTerminal(string(rec.Status)) {
			t.Fatalf("job %s ended %s: %v", id, rec.Status, rec.Err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %s stayed %s, want %s", id, rec.Status, want)
		}
		time.Sleep(timelinePoll)
	}
}

// waitIdleJobs waits until no job is queued or running.
func waitIdleJobs(t *testing.T, store job.Store) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		left, err := store.Unfinished(t.Context())
		if err != nil {
			t.Fatalf("list jobs: %v", err)
		}
		if len(left) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("jobs still running: %d", len(left))
		}
		time.Sleep(timelinePoll)
	}
}

// setJobStatus writes one terminal status onto an existing job row.
func setJobStatus(t *testing.T, fx *wireFixture, id string, status job.Status) {
	t.Helper()
	res, err := fx.db.Writer().ExecContext(t.Context(),
		`UPDATE jobs SET status = ? WHERE id = ?`, string(status), id)
	if err != nil {
		t.Fatalf("set job status: %v", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		t.Fatalf("set job status: %v", err)
	}
	if n != 1 {
		t.Fatalf("set job status updated %d rows, want 1", n)
	}
}

// TestPartialTimelineWhileReconcileRunsStillMerges keeps a reconcile
// job running while the claimed file holds only a prefix. The wait
// stays pending. The finished document then merges and uploads once.
func TestPartialTimelineWhileReconcileRunsStillMerges(t *testing.T) {
	const owner = "owner-partial-timeline"
	fx, drafts, episodeID, sessionID, uploads := newOffsetEpisode(t, owner)
	prefix := []byte(`{"turns":`)
	if _, err := repliesFromTimeline(prefix); err == nil {
		t.Fatal("prefix parsed, want a partial document")
	}
	plantReconcile(t, drafts, "reconcile-partial", owner, episodeID, job.StatusRunning)
	timelineID, err := fx.media.Persist(t.Context(), bytes.NewReader(prefix), mediastore.Put{
		ContentType: "application/json",
		Owner:       owner,
		Group:       episodeID,
		Visibility:  mediastore.Private,
	})
	if err != nil {
		t.Fatalf("persist prefix: %v", err)
	}
	insertClaim(t, fx, sessionID, 0, timelineID)
	replies, pending, err := drafts.hostReplies(t.Context(), owner, episodeID)
	if err != nil || !pending || len(replies) != 0 {
		t.Fatalf("prefix replies = %v pending %v err %v, want pending", replies, pending, err)
	}
	jobID, started, err := drafts.startTranscript(t.Context(), owner, episodeID)
	if err != nil || !started || jobID == "" {
		t.Fatalf("start transcript = %q %v err %v, want a job", jobID, started, err)
	}
	for range 25 {
		rec, err := drafts.store.Get(t.Context(), jobID)
		if err != nil {
			t.Fatalf("read job: %v", err)
		}
		if jobTerminal(string(rec.Status)) {
			t.Fatalf("job ended %s on the prefix: %v", rec.Status, rec.Err)
		}
		replies, pending, err = drafts.hostReplies(t.Context(), owner, episodeID)
		if err != nil || !pending || len(replies) != 0 {
			t.Fatalf("prefix replies = %v pending %v err %v, want pending", replies, pending, err)
		}
		time.Sleep(timelinePoll)
	}
	if uploads.Load() != 0 {
		t.Fatalf("uploads = %d, want none while the file is a prefix", uploads.Load())
	}
	replaceTimelineBlob(t, fx, timelineID, twoReplyTimeline(t))
	replies, pending, err = drafts.hostReplies(t.Context(), owner, episodeID)
	if err != nil || pending || len(replies) != 2 {
		t.Fatalf("finished replies = %v pending %v err %v, want two replies", replies, pending, err)
	}
	waitJobStatus(t, drafts.store, jobID, job.StatusDone)
	if uploads.Load() != 1 {
		t.Fatalf("uploads = %d, want one batch", uploads.Load())
	}
	assertWords(t, loadEditWords(t, fx, episodeID), mergedReplyWords())
	if err := drafts.store.Finish(t.Context(), job.Record{
		ID: "reconcile-partial", Status: job.StatusDone, UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("finish reconcile: %v", err)
	}
	waitIdleJobs(t, drafts.store)
}

// TestTerminalReconcileDoesNotBlockTheGuestBatch runs the guest batch
// when reconcile has ended in error, settled is still 0, and the
// timeline url is empty. The job uploads once and stores no host words.
func TestTerminalReconcileDoesNotBlockTheGuestBatch(t *testing.T) {
	const owner = "owner-reconcile-error"
	fx, drafts, episodeID, sessionID, uploads := newOffsetEpisode(t, owner)
	insertClaim(t, fx, sessionID, 0, "")
	plantReconcile(t, drafts, "reconcile-ended", owner, episodeID, job.StatusError)
	jobID, started, err := drafts.startTranscript(t.Context(), owner, episodeID)
	if err != nil || !started || jobID == "" {
		t.Fatalf("start transcript = %q %v err %v, want a job", jobID, started, err)
	}
	waitJobStatus(t, drafts.store, jobID, job.StatusDone)
	if uploads.Load() != 1 {
		t.Fatalf("uploads = %d, want one batch", uploads.Load())
	}
	stored := loadEditWords(t, fx, episodeID)
	if len(stored) != 1 || stored[0].Text != "guest" || stored[0].StartMs != 120 || stored[0].EndMs != 270 {
		t.Fatalf("stored = %+v, want only the guest word", stored)
	}
	waitIdleJobs(t, drafts.store)
}

// TestHostWaitRunsTheGuestBatchAlone shortens the host wait to a few
// polls with no reconcile job, so the wait returns empty replies and
// no error. Without the bound the wait hangs until the context ends.
func TestHostWaitRunsTheGuestBatchAlone(t *testing.T) {
	const owner = "owner-host-wait-alone"
	fx, drafts, episodeID, sessionID, _ := newOffsetEpisode(t, owner)
	insertClaim(t, fx, sessionID, 0, "")
	drafts.hostWait = 3 * timelinePoll
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	replies, err := drafts.awaitHostReplies(ctx, owner, episodeID)
	if err != nil {
		t.Fatalf("await = err %v, want empty replies and nil error", err)
	}
	if len(replies) != 0 {
		t.Fatalf("await = %v, want empty replies", replies)
	}
}

// TestHostRepliesWaitCoversSettleWaits keeps the transcript host wait
// past both reconcile backoffs with room, so the batch still sees a
// timeline the settle stored late. The two sums mirror the close and
// artifact backoffs the broker waits through.
func TestHostRepliesWaitCoversSettleWaits(t *testing.T) {
	const closeBackoff = 500*time.Millisecond + time.Second + 2*time.Second + 4*time.Second + 8*time.Second + 8*time.Second
	const artifactBackoff = time.Second + 2*time.Second + 4*time.Second + 8*time.Second + 16*time.Second + 16*time.Second
	const want = closeBackoff + artifactBackoff + 10*time.Second
	if hostRepliesWait < want {
		t.Fatalf("host wait %s, want at least %s", hostRepliesWait, want)
	}
}

// TestHostWaitReturnsStoredRepliesBeforeTheBound stores the timeline
// before the bound, so the wait returns both host replies.
func TestHostWaitReturnsStoredRepliesBeforeTheBound(t *testing.T) {
	const owner = "owner-host-wait-stored"
	fx, drafts, episodeID, sessionID, _ := newOffsetEpisode(t, owner)
	timelineID, err := fx.media.Persist(t.Context(), bytes.NewReader(twoReplyTimeline(t)), mediastore.Put{
		ContentType: "application/json",
		Owner:       owner,
		Group:       episodeID,
		Visibility:  mediastore.Private,
	})
	if err != nil {
		t.Fatalf("persist timeline: %v", err)
	}
	insertClaim(t, fx, sessionID, 0, timelineID)
	replies, err := drafts.awaitHostReplies(t.Context(), owner, episodeID)
	if err != nil {
		t.Fatalf("await = err %v, want two replies", err)
	}
	if len(replies) != 2 {
		t.Fatalf("await = %v, want two replies", replies)
	}
}

// TestRestartDuringTheWaitStillMerges interrupts a waiting transcript
// before any upload, then stores the two replies. A later schedule
// runs one batch and merges those words.
func TestRestartDuringTheWaitStillMerges(t *testing.T) {
	const owner = "owner-interrupted-wait"
	fx, drafts, episodeID, sessionID, uploads := newOffsetEpisode(t, owner)
	insertClaim(t, fx, sessionID, 0, "")
	jobID, started, err := drafts.startTranscript(t.Context(), owner, episodeID)
	if err != nil || !started || jobID == "" {
		t.Fatalf("start transcript = %q %v err %v, want a job", jobID, started, err)
	}
	if err := drafts.runner.Cancel(jobID); err != nil {
		t.Fatalf("cancel wait: %v", err)
	}
	waitJobStatus(t, drafts.store, jobID, job.StatusCancelled)
	if uploads.Load() != 0 {
		t.Fatalf("uploads = %d, want none before the timeline", uploads.Load())
	}
	setJobStatus(t, fx, jobID, job.StatusInterrupted)
	rec, err := drafts.store.Get(t.Context(), jobID)
	if err != nil {
		t.Fatalf("read interrupted job: %v", err)
	}
	if rec.Status != job.StatusInterrupted {
		t.Fatalf("status = %s, want interrupted", rec.Status)
	}
	if rec.Progress.Stage == transcriptBatchStage {
		t.Fatal("wait reached the batch, want a wait that never uploaded")
	}
	timelineID, err := fx.media.Persist(t.Context(), bytes.NewReader(twoReplyTimeline(t)), mediastore.Put{
		ContentType: "application/json",
		Owner:       owner,
		Group:       episodeID,
		Visibility:  mediastore.Private,
	})
	if err != nil {
		t.Fatalf("persist timeline: %v", err)
	}
	if _, err := fx.db.Writer().ExecContext(t.Context(),
		`UPDATE reconcile_state SET timeline_media_id = ? WHERE session_id = ?`,
		timelineID, sessionID); err != nil {
		t.Fatalf("store timeline id: %v", err)
	}
	again, scheduled, err := drafts.ensureTranscript(t.Context(), owner, episodeID)
	if err != nil || !scheduled || again == "" || again == jobID {
		t.Fatalf("ensure = %q scheduled %v err %v, want a new pass", again, scheduled, err)
	}
	waitJobStatus(t, drafts.store, again, job.StatusDone)
	if uploads.Load() != 1 {
		t.Fatalf("uploads = %d, want one batch", uploads.Load())
	}
	assertWords(t, loadEditWords(t, fx, episodeID), mergedReplyWords())
	if n := kindJobsFor(t, fx, kindEditTranscript, episodeID); n != 2 {
		t.Fatalf("transcript jobs = %d, want the interrupted wait and one pass", n)
	}
	waitIdleJobs(t, drafts.store)
}

// TestInterruptedUploadIsNotScheduledAgain leaves an interrupted
// transcript that already reached the batch. The schedule starts
// nothing, so the guest audio is not uploaded again.
func TestInterruptedUploadIsNotScheduledAgain(t *testing.T) {
	const owner = "owner-interrupted-batch"
	fx, drafts, episodeID, _, uploads := newOffsetEpisode(t, owner)
	detail, err := json.Marshal(episodeDescriptor{OwnerID: owner, EpisodeID: episodeID})
	if err != nil {
		t.Fatalf("encode linkage: %v", err)
	}
	if err := drafts.store.Create(t.Context(), job.Record{
		ID: "job-uploaded", Kind: kindEditTranscript, Status: job.StatusInterrupted,
		Attempt: 1, RootID: "job-uploaded",
		Progress:  job.Progress{Stage: transcriptBatchStage, Detail: detail},
		UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("plant interrupted batch: %v", err)
	}
	again, scheduled, err := drafts.ensureTranscript(t.Context(), owner, episodeID)
	if err != nil || scheduled || again != "" {
		t.Fatalf("ensure = %q scheduled %v err %v, want no second batch", again, scheduled, err)
	}
	if uploads.Load() != 0 {
		t.Fatalf("uploads = %d, want none", uploads.Load())
	}
	if n := kindJobsFor(t, fx, kindEditTranscript, episodeID); n != 1 {
		t.Fatalf("transcript jobs = %d, want the interrupted batch alone", n)
	}
}

// openPreviewJobs opens a runner with the preview kind over the fixture
// database and returns the scheduler around it.
func openPreviewJobs(t *testing.T, fx *wireFixture) *jobs {
	t.Helper()
	jobStore, err := jobsqlitestore.Open(t.Context(), jobsqlitestore.Config{DB: fx.db})
	if err != nil {
		t.Fatalf("open job store: %v", err)
	}
	events := stream.New(stream.Config{})
	resolver := &render.Resolver{
		DB:      fx.db.Writer(),
		Media:   render.StoreMedia(fx.media, fx.index),
		WorkDir: fx.pipe.renderWorkDir,
		Locate:  fx.pipe.locateStems,
	}
	previewKind, err := (&jobs{resolver: resolver, renderConcurrency: 1}).previewKind()
	if err != nil {
		t.Fatalf("preview kind: %v", err)
	}
	runner, err := job.Open(t.Context(), job.Config{
		Broker: events,
		Store:  jobStore,
		Kinds:  map[string]job.Kind{kindPreview: previewKind},
	})
	if err != nil {
		t.Fatalf("open runner: %v", err)
	}
	return &jobs{runner: runner, resolver: resolver, pipe: fx.pipe, store: jobStore}
}

// TestPreviewSchedulesRunsAndStoresRow schedules one preview for a draft
// episode with both stems linked, and requires a repeat to start
// nothing. The run mixes the stems and stores the preview row, so the
// detail serves its address and a later completion meets the row.
func TestPreviewSchedulesRunsAndStoresRow(t *testing.T) {
	fx := openWireFixture(t)
	const owner = "owner-preview-schedule"
	insertWireUser(t, fx, owner)
	drafts := openPreviewJobs(t, fx)
	ctx := t.Context()
	episodeID := draftEpisode(t, fx, owner)
	jobID, scheduled, err := drafts.ensurePreview(ctx, owner, episodeID)
	if err != nil {
		t.Fatalf("ensure preview: %v", err)
	}
	if !scheduled || jobID == "" {
		t.Fatalf("scheduled = %v job %q, want a preview start", scheduled, jobID)
	}
	if again, covered, err := drafts.ensurePreview(ctx, owner, episodeID); err != nil || covered || again != "" {
		t.Fatalf("repeat ensure = %q, %v, %v, want no second start", again, covered, err)
	}
	waitJobStatus(t, drafts.store, jobID, job.StatusDone)
	mediaID, err := render.PreviewMediaID(ctx, fx.db.Writer(), owner, episodeID)
	if err != nil {
		t.Fatalf("preview media: %v", err)
	}
	if mediaID == "" {
		t.Fatal("preview row holds no blob after a done run")
	}
	blob, err := render.StoreMedia(fx.media, fx.index).Get(ctx, mediaID)
	if err != nil {
		t.Fatalf("read preview blob: %v", err)
	}
	if blob.ContentType != render.OpusContentType {
		t.Fatalf("preview content type = %q, want the streaming opus", blob.ContentType)
	}
	if again, covered, err := drafts.ensurePreview(ctx, owner, episodeID); err != nil || covered || again != "" {
		t.Fatalf("row ensure = %q, %v, %v, want no start beside the row", again, covered, err)
	}
	if n := kindJobsFor(t, fx, kindPreview, episodeID); n != 1 {
		t.Fatalf("preview jobs = %d, want the one pass alone", n)
	}
	waitIdleJobs(t, drafts.store)
}

// TestPreviewKindRefusesDuplicateRegistration registers the preview kind
// twice and requires the boot to refuse. A second registration would
// silently replace the first limit and resume policy.
func TestPreviewKindRefusesDuplicateRegistration(t *testing.T) {
	core := map[string]job.Kind{kindPreview: {Limit: 1}}
	if _, err := mergeKinds(core, map[string]job.Kind{kindPreview: {Limit: 1}}); !errors.Is(err, errDuplicateKind) {
		t.Fatalf("duplicate preview kind merged with err = %v, want the boot refusal", err)
	}
}

// createCapture records the last creation body one adapter sent.
type createCapture struct {
	mu   sync.Mutex
	body map[string]any
}

// capturingBatch answers the upload and one queued transcript while it
// records the creation body. The follow-up fetch completes, and the
// fetch after delete reads the deletion marker.
func capturingBatch(t *testing.T, seen *createCapture, completed string) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	gets := 0
	deleted := `{"id":"tx-cap","status":"completed","text":"Deleted by user.",` +
		`"audio_url":"http://deleted_by_user","confidence":null,"words":null}`
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/upload", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"upload_url":"http://`+r.Host+`/audio/cap"}`)
	})
	mux.HandleFunc("/v2/transcript", func(w http.ResponseWriter, r *http.Request) {
		var decoded map[string]any
		_ = json.NewDecoder(r.Body).Decode(&decoded)
		seen.mu.Lock()
		seen.body = decoded
		seen.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"tx-cap","status":"queued"}`)
	})
	mux.HandleFunc("/v2/transcript/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusOK)
			return
		}
		mu.Lock()
		gets++
		body := deleted
		if gets == 1 {
			body = completed
		}
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// captureClient points one batch client at the capturing server.
func captureClient(t *testing.T, srv *httptest.Server) *assemblyai.BatchClient {
	t.Helper()
	client, err := assemblyai.NewBatchClient(assemblyai.Config{
		BaseURL: srv.URL,
		APIKey:  "probe-key",
		Client:  srv.Client(),
	})
	if err != nil {
		t.Fatalf("open batch client: %v", err)
	}
	return client
}

// TestAnalysisCreateAsksForEntitiesAndPhrases runs the analysis adapter
// creation call and requires both feature flags set, because the thread
// panel builds from what they store.
func TestAnalysisCreateAsksForEntitiesAndPhrases(t *testing.T) {
	t.Parallel()
	seen := &createCapture{}
	completed := `{"id":"tx-cap","status":"completed","text":"Mara","audio_duration":2,` +
		`"words":[{"text":"Mara","start":100,"end":300,"confidence":0.9,"speaker":null}]}`
	srv := capturingBatch(t, seen, completed)
	adapter := batchTranscriber{batch: captureClient(t, srv)}
	id, err := adapter.Create(t.Context(), analysis.CreateRequest{AudioURL: srv.URL + "/audio/cap"})
	if err != nil {
		t.Fatalf("analysis create: %v", err)
	}
	if id != "tx-cap" {
		t.Fatalf("analysis create id = %q, want tx-cap", id)
	}
	seen.mu.Lock()
	defer seen.mu.Unlock()
	if seen.body["entity_detection"] != true {
		t.Fatalf("entity_detection = %v, want true on the analysis pass", seen.body["entity_detection"])
	}
	if seen.body["auto_highlights"] != true {
		t.Fatalf("auto_highlights = %v, want true on the analysis pass", seen.body["auto_highlights"])
	}
}

// TestMapTranscriptCarriesEntitiesAndPhrases maps one batch transcript
// with an entity and a phrase and requires both on the carried result,
// because the analysis pass stores mentions from them.
func TestMapTranscriptCarriesEntitiesAndPhrases(t *testing.T) {
	t.Parallel()
	done := assemblyai.Transcript{
		ID:   "tx-cap",
		Text: "Mara studied the tide charts.",
		Words: []assemblyai.Word{
			{Text: "Mara", StartMs: 100, EndMs: 300, Confidence: 0.9},
		},
		Entities: []assemblyai.Entity{
			{Type: "person_name", Text: "Mara", StartMs: 100, EndMs: 300},
		},
		Phrases: []assemblyai.KeyPhrase{{
			Text: "tide charts", Rank: 0.83, Count: 2,
			Spans: []assemblyai.Span{{StartMs: 400, EndMs: 800}, {StartMs: 1200, EndMs: 1600}},
		}},
	}
	mapped := mapTranscript(done)
	if len(mapped.Entities) != 1 || mapped.Entities[0].Type != "person_name" || mapped.Entities[0].Text != "Mara" {
		t.Fatalf("entities = %+v, want one person_name Mara", mapped.Entities)
	}
	if len(mapped.Phrases) != 1 || mapped.Phrases[0].Text != "tide charts" || len(mapped.Phrases[0].Spans) != 2 {
		t.Fatalf("phrases = %+v, want one tide charts phrase with two spans", mapped.Phrases)
	}
}

// stubPassBudget holds every reservation and settles without a ledger,
// because the flag tests spend nothing real.
type stubPassBudget struct{}

func (stubPassBudget) Reserve(cost.Price) error { return nil }

func (stubPassBudget) Settle(_, _ cost.Price) error { return nil }

func (stubPassBudget) Release(cost.Price) {}

// TestEditCreateAsksForNeitherFeature runs the real edit transcript pass
// against a capturing server and requires neither feature key on its
// creation call, because the edit pass pays for no add-on.
func TestEditCreateAsksForNeitherFeature(t *testing.T) {
	t.Parallel()
	fx := openWireFixture(t)
	const owner = "owner-edit-flags"
	insertWireUser(t, fx, owner)
	diary, err := broker.NewSQLiteDiary(fx.db)
	if err != nil {
		t.Fatalf("open diary: %v", err)
	}
	episodeID, _, err := diary.CreateEpisodeAndSession(t.Context(), owner, 1800)
	if err != nil {
		t.Fatalf("create episode: %v", err)
	}
	seen := &createCapture{}
	completed := `{"id":"tx-cap","status":"completed","text":"guest","audio_duration":1,` +
		`"words":[{"text":"guest","start":100,"end":250,"confidence":0.9,"speaker":null}]}`
	srv := capturingBatch(t, seen, completed)
	if _, err := transcript.Run(t.Context(), transcript.Config{
		DB:           fx.db.Writer(),
		Batch:        captureClient(t, srv),
		Budgets:      stubPassBudget{},
		OwnerID:      owner,
		EpisodeID:    episodeID,
		Audio:        sineWAV(),
		DurationSecs: 2,
		PollInterval: time.Millisecond,
		SaveRaw:      func(context.Context, []byte) error { return nil },
	}); err != nil {
		t.Fatalf("edit run: %v", err)
	}
	seen.mu.Lock()
	defer seen.mu.Unlock()
	if _, ok := seen.body["entity_detection"]; ok {
		t.Fatalf("entity_detection = %v, want the key absent on the edit pass", seen.body["entity_detection"])
	}
	if _, ok := seen.body["auto_highlights"]; ok {
		t.Fatalf("auto_highlights = %v, want the key absent on the edit pass", seen.body["auto_highlights"])
	}
}

// shareProbeSessions ends no provider session. Publish never calls it,
// so the double only satisfies the constructor.
type shareProbeSessions struct{}

// TerminateSession reports a clean delete the test never triggers.
func (shareProbeSessions) TerminateSession(context.Context, string) (assemblyai.TerminateResult, error) {
	return assemblyai.TerminateResult{Deleted: true}, nil
}

// shareProbeTranscripts removes no provider copy. Publish never calls
// it, so the double only satisfies the constructor.
type shareProbeTranscripts struct{}

// Delete reports success without touching anything.
func (shareProbeTranscripts) Delete(context.Context, string) error { return nil }

// Get answers one transcript the test never reads.
func (shareProbeTranscripts) Get(_ context.Context, id string) (assemblyai.Transcript, error) {
	return assemblyai.Transcript{ID: id, Status: "completed"}, nil
}

// shareProbeOwns checks ownership against one fixed owner. The publish
// path reads ownership from the request context, so the test names the
// minted guest directly instead of signing a cookie for the service.
type shareProbeOwns struct {
	owner string
}

// Owns reports whether ownerID is the test owner.
func (o shareProbeOwns) Owns(_ context.Context, ownerID string) bool { return ownerID == o.owner }

// seedShareEpisode stores one episode with its render blobs, the way a
// finished take leaves them before publish.
func seedShareEpisode(t *testing.T, fx *wireFixture, owner, episodeID string, number int64) {
	t.Helper()
	ctx := t.Context()
	persist := func(body string) string {
		blob, err := fx.media.Persist(ctx, bytes.NewReader([]byte(body)), mediastore.Put{
			ContentType: "audio/ogg",
			Owner:       owner,
			Group:       episodeID,
			Visibility:  mediastore.Private,
		})
		if err != nil {
			t.Fatalf("persist blob: %v", err)
		}
		return blob
	}
	opus := persist("render opus " + episodeID)
	aac := persist("render aac " + episodeID)
	if _, err := fx.db.Writer().ExecContext(ctx, `INSERT INTO episodes
		(id, owner_id, number, title, state, visibility, share_token, seeded)
		VALUES (?, ?, ?, ?, 'ready', 'private', ?, 0)`,
		episodeID, owner, number, "Episode "+episodeID, "seed-"+episodeID); err != nil {
		t.Fatalf("seed episode: %v", err)
	}
	renderID, err := keelid.New()
	if err != nil {
		t.Fatalf("mint render id: %v", err)
	}
	if _, err := fx.db.Writer().ExecContext(ctx, `INSERT INTO renders
		(id, owner_id, episode_id, input_hash, opus_media_id, aac_media_id, loudness)
		VALUES (?, ?, ?, 'hash', ?, ?, -16)`, renderID, owner, episodeID, opus, aac); err != nil {
		t.Fatalf("seed render: %v", err)
	}
}

// TestPublicDetailReloadCarriesSharePath boots the wired episode routes,
// publishes one episode through the publish service, and requires the
// reloaded detail to carry its share path. A private episode carries
// empty, and the same detail behind a nil token reader carries empty
// too, so reverting the wiring fails this test.
func TestPublicDetailReloadCarriesSharePath(t *testing.T) {
	ctx := t.Context()
	fx := openWireFixture(t)
	identitySvc, err := identity.New(ctx, identity.Config{DB: fx.db, SigningKey: "share-path-test-signing-key"})
	if err != nil {
		t.Fatalf("open identity: %v", err)
	}
	episodeSvc, err := episode.NewService(episode.Config{DB: fx.db})
	if err != nil {
		t.Fatalf("open episode service: %v", err)
	}
	wired := identitySvc.Middleware(episodeRoutes(fx.db, episodeSvc))
	serve := func(handler http.Handler, cookie *http.Cookie, id string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/episodes/"+id, nil)
		if cookie != nil {
			req.AddCookie(cookie)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	first := serve(wired, nil, "missing")
	var cookie *http.Cookie
	for _, c := range first.Result().Cookies() {
		if c.Name == identity.CookieName {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("first detail set no session cookie")
	}
	cookieReq := httptest.NewRequest(http.MethodGet, "/", nil)
	cookieReq.AddCookie(cookie)
	user, err := identitySvc.Resolve(cookieReq)
	if err != nil {
		t.Fatalf("resolve minted guest: %v", err)
	}

	seedShareEpisode(t, fx, user.ID, "ep-public", 1)
	seedShareEpisode(t, fx, user.ID, "ep-private", 2)
	publishSvc, err := privacy.New(privacy.Config{
		DB:          fx.db,
		Media:       fx.media,
		CoverDir:    t.TempDir(),
		Sessions:    shareProbeSessions{},
		Transcripts: shareProbeTranscripts{},
		Owns:        shareProbeOwns{owner: user.ID},
	})
	if err != nil {
		t.Fatalf("open publish service: %v", err)
	}
	token, err := publishSvc.Publish(ctx, "ep-public")
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	shareOf := func(handler http.Handler, id string) string {
		t.Helper()
		rec := serve(handler, cookie, id)
		if rec.Code != http.StatusOK {
			t.Fatalf("detail %s status = %d, want 200: %s", id, rec.Code, rec.Body.String())
		}
		var body struct {
			SharePath string `json:"share_path"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode detail %s: %v", id, err)
		}
		return body.SharePath
	}
	if got := shareOf(wired, "ep-public"); got != "/share/"+token {
		t.Fatalf("public share path = %q, want /share/%s", got, token)
	}
	if got := shareOf(wired, "ep-private"); got != "" {
		t.Fatalf("private share path = %q, want empty", got)
	}

	bare := identitySvc.Middleware(api.NewEpisodesWithPreview(episodeSvc, render.PreviewStore{DB: fx.db.Writer()}))
	if got := shareOf(bare, "ep-public"); got != "" {
		t.Fatalf("nil reader share path = %q, want empty", got)
	}
}
