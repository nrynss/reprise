package main

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/nrynss/keel/cost"
	keelsqlite "github.com/nrynss/keel/sqlite"
	"github.com/nrynss/reprise/internal/host"
	"github.com/nrynss/reprise/internal/settings"
	reprisestore "github.com/nrynss/reprise/internal/store"
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
