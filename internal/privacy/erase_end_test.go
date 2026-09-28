package privacy_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/nrynss/reprise/internal/assemblyai"
)

// heldSocket is a fake voice socket. It records text frames in order with
// the deletes the test server sees. A gone socket answers the end with
// the gone sentinel, the way a closed provider socket does.
type heldSocket struct {
	mu    *sync.Mutex
	order *[]string
	gone  bool
	sent  [][]byte
}

// SendText records one frame, or reports the socket gone.
func (s *heldSocket) SendText(_ context.Context, payload []byte) error {
	if s.gone {
		return assemblyai.ErrSocketGone
	}
	frame := append([]byte(nil), payload...)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, frame)
	if s.order != nil {
		*s.order = append(*s.order, "end")
	}
	return nil
}

// frames returns the recorded frames under the lock.
func (s *heldSocket) frames() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([][]byte(nil), s.sent...)
}

// endDeleteServer records provider deletes in order with the held socket
// frames. An id in known deletes. Any other id answers not found, which
// the end still counts as success.
func endDeleteServer(t *testing.T, mu *sync.Mutex, order *[]string, deletes *int, known map[string]bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		mu.Lock()
		*deletes++
		*order = append(*order, "delete")
		mu.Unlock()
		if !known[r.URL.Path] {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"code":"session_not_found","message":"Session not found"}`))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
}

// TestEraseEndsLiveSocketBeforeDelete pins the erase order. A held socket
// sees one session.end frame before the provider delete. Reverting the
// erase to the plain delete leaves the socket empty and fails here.
func TestEraseEndsLiveSocketBeforeDelete(t *testing.T) {
	var mu sync.Mutex
	var order []string
	deletes := 0
	server := endDeleteServer(t, &mu, &order, &deletes,
		map[string]bool{"/v1/sessions/prov-session-owner-a": true})
	t.Cleanup(server.Close)
	client, err := assemblyai.NewSessionsClient(server.URL, "test-key", server.Client())
	if err != nil {
		t.Fatalf("new sessions client: %v", err)
	}
	sock := &heldSocket{mu: &mu, order: &order}
	if err := client.HoldSocket("prov-session-owner-a", sock); err != nil {
		t.Fatalf("hold socket: %v", err)
	}
	fx := openFixtureOn(t, client)
	fx.as("owner-a")
	gone := fx.seeds["owner-a"]

	jobID, err := fx.svc.Erase(t.Context(), gone.episode)
	if err != nil {
		t.Fatalf("erase: %v", err)
	}
	fx.waitJobDone(t, jobID)

	got := sock.frames()
	if len(got) != 1 || string(got[0]) != `{"type":"session.end"}` {
		t.Fatalf("socket frames %q, want one session.end", got)
	}
	mu.Lock()
	sequence := append([]string(nil), order...)
	count := deletes
	mu.Unlock()
	if len(sequence) != 2 || sequence[0] != "end" || sequence[1] != "delete" {
		t.Fatalf("order %v, want end then delete", sequence)
	}
	if count != 1 {
		t.Fatalf("server saw %d deletes, want exactly one", count)
	}
	if total := fx.count(t, "episodes", "owner-a"); total != 0 {
		t.Fatalf("episodes holds %d owner-a rows, want none", total)
	}
	rep, err := fx.svc.Eraser().Inspect(t.Context(), fx.runner, jobID)
	if err != nil {
		t.Fatalf("inspect erasure: %v", err)
	}
	if !rep.Complete() {
		t.Fatalf("erasure still owes %v", rep.Stuck)
	}
}

// TestEraseDeletesWhenSocketAndRecordGone pins the erase fallback. A
// socket that is already gone and a record the provider no longer keeps
// still finish the erasure, with the episode rows gone and the job done.
func TestEraseDeletesWhenSocketAndRecordGone(t *testing.T) {
	var mu sync.Mutex
	var order []string
	deletes := 0
	server := endDeleteServer(t, &mu, &order, &deletes, map[string]bool{})
	t.Cleanup(server.Close)
	client, err := assemblyai.NewSessionsClient(server.URL, "test-key", server.Client())
	if err != nil {
		t.Fatalf("new sessions client: %v", err)
	}
	if err := client.HoldSocket("prov-session-owner-a", &heldSocket{mu: &mu, gone: true}); err != nil {
		t.Fatalf("hold socket: %v", err)
	}
	fx := openFixtureOn(t, client)
	fx.as("owner-a")
	gone := fx.seeds["owner-a"]

	jobID, err := fx.svc.Erase(t.Context(), gone.episode)
	if err != nil {
		t.Fatalf("erase: %v", err)
	}
	fx.waitJobDone(t, jobID)

	mu.Lock()
	sequence := append([]string(nil), order...)
	count := deletes
	mu.Unlock()
	if len(sequence) != 1 || sequence[0] != "delete" {
		t.Fatalf("order %v, want only the delete", sequence)
	}
	if count != 1 {
		t.Fatalf("server saw %d deletes, want exactly one", count)
	}
	if total := fx.count(t, "episodes", "owner-a"); total != 0 {
		t.Fatalf("episodes holds %d owner-a rows, want none", total)
	}
	rep, err := fx.svc.Eraser().Inspect(t.Context(), fx.runner, jobID)
	if err != nil {
		t.Fatalf("inspect erasure: %v", err)
	}
	if !rep.Complete() {
		t.Fatalf("erasure still owes %v", rep.Stuck)
	}
}
