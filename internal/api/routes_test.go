package api

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nrynss/keel/cost"
	"github.com/nrynss/keel/gate"
	"github.com/nrynss/keel/stream"
)

// updateGoldens rewrites the golden files the browser decodes. Run the
// package with -update once after an intended route change, then commit the
// result. Ordinary runs compare and never write.
var updateGoldens = flag.Bool("update", false, "rewrite the golden files")

// goldenDir holds the responses the server writes and the browser decodes.
// One directory serves both sides, so neither copy can drift unnoticed.
const goldenDir = "../../web/src/lib/api/testdata"

// expectedTable pins the route table entry by entry. Adding, removing or
// reordering a route fails here first, with the diff naming the change.
var expectedTable = []Route{
	{Method: "POST", Pattern: "/api/sessions"},
	{Method: "POST", Pattern: "/api/sessions/{id}/end"},
	{Method: "", Pattern: "/api/uploads/"},
	{Method: "GET", Pattern: "/api/episodes"},
	{Method: "GET", Pattern: "/api/episodes/{id}"},
	{Method: "POST", Pattern: "/api/episodes/{id}/decisions"},
	{Method: "POST", Pattern: "/api/episodes/{id}/done"},
	{Method: "GET", Pattern: "/api/jobs/{id}/events"},
	{Method: "POST", Pattern: "/api/episodes/{id}/publish"},
	{Method: "DELETE", Pattern: "/api/episodes/{id}/publish"},
	{Method: "DELETE", Pattern: "/api/episodes/{id}"},
	{Method: "GET", Pattern: "/api/threads"},
	{Method: "GET", Pattern: "/api/admin/limits"},
	{Method: "POST", Pattern: "/api/admin/limits/pause"},
	{Method: "POST", Pattern: "/api/admin/limits/owner"},
	{Method: "GET", Pattern: "/media/{id}"},
}

func TestRouteTableMatchesExpectation(t *testing.T) {
	if !reflect.DeepEqual(Routes(), expectedTable) {
		t.Fatalf("route table drifted:\n got %+v\nwant %+v", Routes(), expectedTable)
	}
}

// errorShape mirrors the shared envelope body. The test branches on the
// code and only checks that the message is present, never its wording.
type errorShape struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// requestFor builds one request per table entry. Wildcards become a plain
// id. An empty method defaults to GET for the table pass, and the upload
// subtree gets its own pass below with the methods it will serve.
func requestFor(route Route) *http.Request {
	method := route.Method
	if method == "" {
		method = http.MethodGet
	}
	path := strings.ReplaceAll(route.Pattern, "{id}", "e01")
	return httptest.NewRequest(method, path, nil)
}

func TestStubsRefuseWithNotImplemented(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux)
	for _, route := range Routes() {
		req := requestFor(route)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		res := rec.Result()
		if res.StatusCode != http.StatusNotImplemented {
			t.Fatalf("%s %s: status %d, want 501", req.Method, req.URL.Path, res.StatusCode)
		}
		var body errorShape
		if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
			t.Fatalf("%s %s: decode refusal: %v", req.Method, req.URL.Path, err)
		}
		if err := res.Body.Close(); err != nil {
			t.Fatalf("%s %s: close body: %v", req.Method, req.URL.Path, err)
		}
		if body.Error.Code != CodeNotImplemented {
			t.Fatalf("%s %s: code %q, want %q", req.Method, req.URL.Path, body.Error.Code, CodeNotImplemented)
		}
		if body.Error.Message == "" {
			t.Fatalf("%s %s: refusal carries no message", req.Method, req.URL.Path)
		}
	}
}

func TestUploadSubtreeRefusesEveryMethod(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux)
	paths := []string{"/api/uploads/", "/api/uploads/e01", "/api/uploads/e01/chunks/0", "/api/uploads/e01/complete"}
	methods := []string{http.MethodGet, http.MethodPost, http.MethodPut}
	for _, path := range paths {
		for _, method := range methods {
			req := httptest.NewRequest(method, path, nil)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusNotImplemented {
				t.Fatalf("%s %s: status %d, want 501", method, path, rec.Code)
			}
		}
	}
}

func TestGoldenRouteTable(t *testing.T) {
	raw, err := json.Marshal(Routes())
	if err != nil {
		t.Fatalf("marshal route table: %v", err)
	}
	checkGolden(t, "routes.json", append(raw, '\n'))
}

func TestGoldenStubRefusal(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux)
	req := httptest.NewRequest(http.MethodGet, "/api/episodes", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status %d, want 501", rec.Code)
	}
	checkGolden(t, "stub-refusal.json", rec.Body.Bytes())
}

// checkGolden compares content against the named golden file. With -update
// it writes the file instead. Either way the golden files stay written by
// the server, so the browser decodes server output and never a copy.
func checkGolden(t *testing.T, name string, content []byte) {
	t.Helper()
	path := filepath.Join(goldenDir, name)
	if *updateGoldens {
		if err := os.MkdirAll(goldenDir, 0o755); err != nil {
			t.Fatalf("create golden dir: %v", err)
		}
		if err := os.WriteFile(path, content, 0o644); err != nil {
			t.Fatalf("write golden %s: %v", name, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v (rerun with -update to create it)", name, err)
	}
	if string(want) != string(content) {
		t.Fatalf("golden %s drifted:\n got %s\nwant %s", name, content, want)
	}
}

// guestStub stands in for the guest session middleware. It counts calls
// and marks the response, so tests see whether the chain ran and in what
// order.
type guestStub struct {
	calls *int
}

// Middleware marks the response and calls next.
func (g guestStub) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*g.calls++
		w.Header().Set("X-Guest-Middleware", "ran")
		next.ServeHTTP(w, r)
	})
}

// recordingHandler stands in for an implemented handler. It counts calls
// and answers 200, so tests see whether the chain reached it.
type recordingHandler struct {
	calls *int
}

// ServeHTTP counts the call and answers 200.
func (h recordingHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	*h.calls++
	w.WriteHeader(http.StatusOK)
}

// openGate returns a gate with no passcode and one permissive rule, so an
// honest request passes and only the chain order is under test.
func openGate(t *testing.T) (*gate.Gate, gate.Rule) {
	t.Helper()
	g, err := gate.New(gate.Config{})
	if err != nil {
		t.Fatalf("open gate: %v", err)
	}
	rule := gate.Rule{
		Name:      "test",
		PerClient: gate.Limit{Burst: 64, Every: time.Second},
		Global:    gate.Limit{Burst: 64, Every: time.Second},
	}
	return g, rule
}

// mountedDeps wires every mountable route to one recording handler behind
// an open gate and the guest stub. Both owner ceilings agree.
func mountedDeps(t *testing.T, guestCalls, handlerCalls *int) Dependencies {
	t.Helper()
	g, rule := openGate(t)
	return Dependencies{
		Gate:              g,
		Rule:              rule,
		Identity:          guestStub{calls: guestCalls},
		Sessions:          recordingHandler{calls: handlerCalls},
		Episodes:          recordingHandler{calls: handlerCalls},
		SessionEnd:        recordingHandler{calls: handlerCalls},
		Threads:           recordingHandler{calls: handlerCalls},
		Admin:             recordingHandler{calls: handlerCalls},
		Uploads:           recordingHandler{calls: handlerCalls},
		Media:             recordingHandler{calls: handlerCalls},
		Events:            stream.New(stream.Config{}),
		OwnerSessionLimit: cost.Price(100),
		OwnerDefaultLimit: cost.Price(100),
	}
}

func TestMountRefusesUnequalOwnerLimits(t *testing.T) {
	g, rule := openGate(t)
	calls := 0
	deps := Dependencies{
		Gate:              g,
		Rule:              rule,
		Identity:          guestStub{calls: &calls},
		Sessions:          recordingHandler{calls: &calls},
		OwnerSessionLimit: cost.Price(100),
		OwnerDefaultLimit: cost.Price(200),
	}
	if err := Mount(http.NewServeMux(), deps); !errors.Is(err, ErrMount) {
		t.Fatalf("mount with unequal limits: error %v, want %v", err, ErrMount)
	}
	deps.Sessions = nil
	deps.Admin = recordingHandler{calls: &calls}
	if err := Mount(http.NewServeMux(), deps); !errors.Is(err, ErrMount) {
		t.Fatalf("mount admin with unequal limits: error %v, want %v", err, ErrMount)
	}
}

func TestMountNeedsGateAndIdentity(t *testing.T) {
	_, rule := openGate(t)
	calls := 0
	deps := Dependencies{
		Rule:              rule,
		Identity:          guestStub{calls: &calls},
		Sessions:          recordingHandler{calls: &calls},
		OwnerSessionLimit: cost.Price(100),
		OwnerDefaultLimit: cost.Price(100),
	}
	if err := Mount(http.NewServeMux(), deps); !errors.Is(err, ErrMount) {
		t.Fatalf("mount without gate: error %v, want %v", err, ErrMount)
	}
	g, _ := openGate(t)
	deps.Gate = g
	deps.Identity = nil
	if err := Mount(http.NewServeMux(), deps); !errors.Is(err, ErrMount) {
		t.Fatalf("mount without guest middleware: error %v, want %v", err, ErrMount)
	}
}

func TestMountServesHandlersThroughChain(t *testing.T) {
	guestCalls := 0
	handlerCalls := 0
	mux := http.NewServeMux()
	if err := Mount(mux, mountedDeps(t, &guestCalls, &handlerCalls)); err != nil {
		t.Fatalf("mount: %v", err)
	}
	chained := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/sessions"},
		{http.MethodPost, "/api/sessions/e01/end"},
		{http.MethodGet, "/api/episodes"},
		{http.MethodGet, "/api/episodes/e01"},
		{http.MethodPost, "/api/episodes/e01/decisions"},
		{http.MethodPost, "/api/episodes/e01/done"},
		{http.MethodGet, "/api/threads"},
		{http.MethodGet, "/api/admin/limits"},
		{http.MethodPost, "/api/admin/limits/pause"},
		{http.MethodPost, "/api/admin/limits/owner"},
		{http.MethodPost, "/api/uploads"},
		{http.MethodPut, "/api/uploads/e01/chunks/0"},
		{http.MethodGet, "/media/blob1"},
	}
	for _, tc := range chained {
		guestCalls = 0
		handlerCalls = 0
		req := httptest.NewRequest(tc.method, tc.path, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s %s: status %d, want 200", tc.method, tc.path, rec.Code)
		}
		if guestCalls != 1 {
			t.Fatalf("%s %s: guest middleware ran %d times, want 1", tc.method, tc.path, guestCalls)
		}
		if handlerCalls != 1 {
			t.Fatalf("%s %s: handler ran %d times, want 1", tc.method, tc.path, handlerCalls)
		}
		if rec.Header().Get("X-Guest-Middleware") != "ran" {
			t.Fatalf("%s %s: guest middleware mark missing", tc.method, tc.path)
		}
	}
}

func TestMountStreamsJobEvents(t *testing.T) {
	guestCalls := 0
	handlerCalls := 0
	mux := http.NewServeMux()
	if err := Mount(mux, mountedDeps(t, &guestCalls, &handlerCalls)); err != nil {
		t.Fatalf("mount: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/jobs/e01/events", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("job events: status %d, want 200", rec.Code)
	}
	if contentType := rec.Header().Get("Content-Type"); contentType != "text/event-stream" {
		t.Fatalf("job events: content type %q, want text/event-stream", contentType)
	}
	if guestCalls != 1 {
		t.Fatalf("job events: guest middleware ran %d times, want 1", guestCalls)
	}
}

func TestMountKeepsStubsForUnmountedRoutes(t *testing.T) {
	guestCalls := 0
	handlerCalls := 0
	mux := http.NewServeMux()
	if err := Mount(mux, mountedDeps(t, &guestCalls, &handlerCalls)); err != nil {
		t.Fatalf("mount: %v", err)
	}
	stubs := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/episodes/e01/publish"},
		{http.MethodDelete, "/api/episodes/e01/publish"},
		{http.MethodDelete, "/api/episodes/e01"},
	}
	for _, tc := range stubs {
		beforeGuests := guestCalls
		beforeHandlers := handlerCalls
		req := httptest.NewRequest(tc.method, tc.path, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotImplemented {
			t.Fatalf("%s %s: status %d, want 501", tc.method, tc.path, rec.Code)
		}
		var body errorShape
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("%s %s: decode refusal: %v", tc.method, tc.path, err)
		}
		if body.Error.Code != CodeNotImplemented {
			t.Fatalf("%s %s: code %q, want %q", tc.method, tc.path, body.Error.Code, CodeNotImplemented)
		}
		if guestCalls != beforeGuests || handlerCalls != beforeHandlers {
			t.Fatalf("%s %s: stub ran the chain", tc.method, tc.path)
		}
	}
}

func TestMountGateRefusesBeforeMiddleware(t *testing.T) {
	g, err := gate.New(gate.Config{Passcode: "owner-secret"})
	if err != nil {
		t.Fatalf("open gate: %v", err)
	}
	rule := gate.Rule{
		Name:      "test",
		PerClient: gate.Limit{Burst: 64, Every: time.Second},
		Global:    gate.Limit{Burst: 64, Every: time.Second},
	}
	guestCalls := 0
	handlerCalls := 0
	mux := http.NewServeMux()
	deps := Dependencies{
		Gate:              g,
		Rule:              rule,
		Identity:          guestStub{calls: &guestCalls},
		Sessions:          recordingHandler{calls: &handlerCalls},
		OwnerSessionLimit: cost.Price(100),
		OwnerDefaultLimit: cost.Price(100),
	}
	if err := Mount(mux, deps); err != nil {
		t.Fatalf("mount: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/sessions", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("passcode refusal: status %d, want 403", rec.Code)
	}
	if guestCalls != 0 {
		t.Fatalf("refused request minted state: guest middleware ran %d times", guestCalls)
	}
	if handlerCalls != 0 {
		t.Fatalf("refused request reached the handler %d times", handlerCalls)
	}
}
