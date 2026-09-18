package assemblyai

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

const terminateOpenDoc = `{"id":"sess_open","agent_id":null,"status":"created",` +
	`"public_close_reason":null,"duration_seconds":null,"config":null,` +
	`"created_at":"2026-09-18T19:33:32.904015Z","ended_at":null,"artifacts":[]}`

const terminateClosedDoc = `{"id":"sess_shut","agent_id":null,"status":"completed",` +
	`"public_close_reason":"client end","duration_seconds":39.640212,"config":null,` +
	`"created_at":"2026-09-18T19:33:32.904015Z","ended_at":"2026-09-18T19:34:12.544227Z",` +
	`"artifacts":[{"type":"audio","url":"https://artifacts.example/rec.ogg","content_type":"audio/ogg"}]}`

func terminateServer(t *testing.T) (*SessionsClient, *int) {
	t.Helper()
	deletes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/sessions/sess_open":
			_, _ = w.Write([]byte(terminateOpenDoc))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/sessions/sess_shut":
			_, _ = w.Write([]byte(terminateClosedDoc))
		case r.Method == http.MethodGet:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"code":"session_not_found","message":"Session not found"}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/sessions/sess_open":
			deletes++
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"code":"session_not_found","message":"Session not found"}`))
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(server.Close)
	client, err := NewSessionsClient(server.URL, "test-key", server.Client())
	if err != nil {
		t.Fatalf("new sessions client: %v", err)
	}
	return client, &deletes
}

func TestFetchSessionReadsOpenAndClosed(t *testing.T) {
	client, _ := terminateServer(t)
	open, err := client.FetchSession(t.Context(), "sess_open")
	if err != nil {
		t.Fatalf("fetch open session: %v", err)
	}
	if open.Status != "created" || open.CloseReason != "" {
		t.Fatalf("open session reads %+v, want created with no reason", open)
	}
	if open.DurationSeconds != nil {
		t.Fatalf("open session duration %v, want nil", *open.DurationSeconds)
	}
	if open.CreatedAt.IsZero() || len(open.Artifacts) != 0 {
		t.Fatalf("open session reads %+v, want a start time and no artifacts", open)
	}
	shut, err := client.FetchSession(t.Context(), "sess_shut")
	if err != nil {
		t.Fatalf("fetch closed session: %v", err)
	}
	if shut.Status != "completed" || shut.CloseReason != "client end" {
		t.Fatalf("closed session reads %+v", shut)
	}
	if shut.DurationSeconds == nil || *shut.DurationSeconds != 39.640212 {
		t.Fatalf("closed session duration %+v, want 39.640212", shut.DurationSeconds)
	}
	if len(shut.Artifacts) != 1 || shut.Artifacts[0].Type != "audio" {
		t.Fatalf("closed session artifacts %+v, want one audio file", shut.Artifacts)
	}
	if _, err := client.FetchSession(t.Context(), "sess_missing"); !errors.Is(err, ErrSessionGone) {
		t.Fatalf("missing session err %v, want ErrSessionGone", err)
	}
	if _, err := client.FetchSession(t.Context(), ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty id err %v, want ErrInvalid", err)
	}
}

func TestTerminateSessionIsIdempotent(t *testing.T) {
	client, deletes := terminateServer(t)
	first, err := client.TerminateSession(t.Context(), "sess_open")
	if err != nil {
		t.Fatalf("terminate session: %v", err)
	}
	if !first.Deleted {
		t.Fatalf("first delete %+v, want Deleted true", first)
	}
	second, err := client.TerminateSession(t.Context(), "sess_missing")
	if err != nil {
		t.Fatalf("repeat delete: %v", err)
	}
	if second.Deleted {
		t.Fatalf("repeat delete %+v, want Deleted false with no error", second)
	}
	if *deletes != 1 {
		t.Fatalf("server saw %d deletes, want exactly one", *deletes)
	}
	if _, err := client.TerminateSession(context.Background(), ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty id err %v, want ErrInvalid", err)
	}
}

func TestTerminateSessionRefusesBadServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	client, err := NewSessionsClient(server.URL, "test-key", server.Client())
	if err != nil {
		t.Fatalf("new sessions client: %v", err)
	}
	if _, err := client.FetchSession(t.Context(), "sess_open"); !errors.Is(err, ErrFetch) {
		t.Fatalf("fetch err %v, want ErrFetch", err)
	}
	if _, err := client.TerminateSession(t.Context(), "sess_open"); !errors.Is(err, ErrTerminate) {
		t.Fatalf("delete err %v, want ErrTerminate", err)
	}
	if _, err := NewSessionsClient("", "key", nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty base err %v, want ErrInvalid", err)
	}
	if _, err := NewSessionsClient("https://agents.example", "", nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty key err %v, want ErrInvalid", err)
	}
}
