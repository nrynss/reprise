package api

import (
	"encoding/json"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
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
