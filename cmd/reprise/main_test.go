package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestHealthzReturnsOK(t *testing.T) {
	rec := httptest.NewRecorder()
	handleHealth(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("healthz status = %d, want 200", rec.Code)
	}
	body, _ := io.ReadAll(rec.Body)
	if len(body) == 0 {
		t.Fatal("healthz body is empty, want the boot id")
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
