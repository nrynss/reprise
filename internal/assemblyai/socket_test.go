package assemblyai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// scriptSocket is a fake voice socket. It records text frames in order
// with the deletes the test server sees.
type scriptSocket struct {
	mu    *sync.Mutex
	order *[]string
	gone  bool
	sent  [][]byte
}

func (s *scriptSocket) SendText(_ context.Context, payload []byte) error {
	if s.gone {
		return ErrSocketGone
	}
	s.sent = append(s.sent, append([]byte(nil), payload...))
	if s.order != nil {
		s.mu.Lock()
		*s.order = append(*s.order, "end")
		s.mu.Unlock()
	}
	return nil
}

func deleteServer(t *testing.T, order *[]string, mu *sync.Mutex) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete && r.URL.Path == "/v1/sessions/sess_open" {
			mu.Lock()
			*order = append(*order, "delete")
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
}

// TestEndSessionWritesEndBeforeDelete pins the live socket order. A
// mutation that only deletes leaves the socket empty and fails here.
func TestEndSessionWritesEndBeforeDelete(t *testing.T) {
	var mu sync.Mutex
	var order []string
	opens := 0
	srv := deleteServer(t, &order, &mu)
	defer srv.Close()
	client, err := NewSessionsClient(srv.URL, "test-key", srv.Client())
	if err != nil {
		t.Fatalf("new sessions client: %v", err)
	}
	client.dial = func(context.Context, string) (LiveSocket, error) {
		opens++
		return nil, errors.New("opened a socket")
	}
	sock := &scriptSocket{mu: &mu, order: &order}
	if err := client.HoldSocket("sess_open", sock); err != nil {
		t.Fatalf("hold socket: %v", err)
	}
	res, err := client.EndSession(t.Context(), "sess_open")
	if err != nil {
		t.Fatalf("end session: %v", err)
	}
	if !res.SocketEnded || !res.Deleted {
		t.Fatalf("end result %+v, want the frame written and the record deleted", res)
	}
	if opens != 0 {
		t.Fatalf("end opened %d sockets, want none when a live socket is held", opens)
	}
	if len(sock.sent) != 1 || string(sock.sent[0]) != `{"type":"session.end"}` {
		t.Fatalf("socket frames %q, want one session.end", sock.sent)
	}
	mu.Lock()
	got := append([]string(nil), order...)
	mu.Unlock()
	if len(got) != 2 || got[0] != "end" || got[1] != "delete" {
		t.Fatalf("order %v, want end then delete", got)
	}
}

// TestEndSessionDeletesWhenSocketGone checks a closed socket still
// deletes and does not open a replacement.
func TestEndSessionDeletesWhenSocketGone(t *testing.T) {
	var mu sync.Mutex
	var order []string
	opens := 0
	srv := deleteServer(t, &order, &mu)
	defer srv.Close()
	client, err := NewSessionsClient(srv.URL, "test-key", srv.Client())
	if err != nil {
		t.Fatalf("new sessions client: %v", err)
	}
	client.dial = func(context.Context, string) (LiveSocket, error) {
		opens++
		return &scriptSocket{}, nil
	}
	if err := client.HoldSocket("sess_open", &scriptSocket{gone: true}); err != nil {
		t.Fatalf("hold socket: %v", err)
	}
	res, err := client.EndSession(t.Context(), "sess_open")
	if err != nil {
		t.Fatalf("end session: %v", err)
	}
	if res.SocketEnded || !res.Deleted {
		t.Fatalf("end result %+v, want a delete and no end frame", res)
	}
	if opens != 0 {
		t.Fatalf("gone socket opened %d replacements, want none", opens)
	}
	mu.Lock()
	got := append([]string(nil), order...)
	mu.Unlock()
	if len(got) != 1 || got[0] != "delete" {
		t.Fatalf("order %v, want only the delete", got)
	}
}

// TestSecondEndDoesNotOpenSocket checks a repeat end deletes again and
// does not dial. The first end is the one that opens.
func TestSecondEndDoesNotOpenSocket(t *testing.T) {
	var mu sync.Mutex
	var order []string
	opens := 0
	srv := deleteServer(t, &order, &mu)
	defer srv.Close()
	client, err := NewSessionsClient(srv.URL, "test-key", srv.Client())
	if err != nil {
		t.Fatalf("new sessions client: %v", err)
	}
	var opened *scriptSocket
	client.dial = func(context.Context, string) (LiveSocket, error) {
		opens++
		opened = &scriptSocket{mu: &mu, order: &order}
		return opened, nil
	}
	first, err := client.EndSession(t.Context(), "sess_open")
	if err != nil {
		t.Fatalf("first end: %v", err)
	}
	if !first.SocketEnded || !first.Deleted || opens != 1 {
		t.Fatalf("first end %+v opens %d, want one open and a delete", first, opens)
	}
	second, err := client.EndSession(t.Context(), "sess_open")
	if err != nil {
		t.Fatalf("second end: %v", err)
	}
	if !second.SocketEnded || !second.Deleted {
		t.Fatalf("second end %+v, want success without a new socket", second)
	}
	if opens != 1 {
		t.Fatalf("second end opened sockets, open count %d, want 1", opens)
	}
	if opened == nil || len(opened.sent) != 1 || string(opened.sent[0]) != `{"type":"session.end"}` {
		t.Fatalf("opened socket frames %v, want one session.end", opened)
	}
}

// TestEndSessionDialFailureSkipsDelete checks a refused dial does not
// delete. The record stays readable for a retry.
func TestEndSessionDialFailureSkipsDelete(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}
	trips := 0
	client, err := NewSessionsClient("http://"+addr, "test-key", &http.Client{
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			trips++
			return nil, errors.New("delete attempted")
		}),
	})
	if err != nil {
		t.Fatalf("new sessions client: %v", err)
	}
	if _, err := client.EndSession(t.Context(), "sess_closed"); err == nil {
		t.Fatal("end after a refused dial succeeded, want an error")
	}
	if trips != 0 {
		t.Fatalf("delete calls %d after a refused dial, want none", trips)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// TestEndSessionResumesThenEndsBeforeDelete drives a fake voice socket.
// The new socket must resume before session.end, and the delete follows.
// A second end must not upgrade again.
func TestEndSessionResumesThenEndsBeforeDelete(t *testing.T) {
	var mu sync.Mutex
	var order []string
	var upgrades int
	var fail string
	note := func(step string) {
		mu.Lock()
		order = append(order, step)
		mu.Unlock()
	}
	setFail := func(msg string) {
		mu.Lock()
		if fail == "" {
			fail = msg
		}
		mu.Unlock()
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/ws" {
			serveVoiceEnd(w, r, &upgrades, &mu, note, setFail)
			return
		}
		if r.Method == http.MethodDelete && r.URL.Path == "/v1/sessions/sess_live" {
			note("delete")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	client, err := NewSessionsClient(srv.URL, "test-key", srv.Client())
	if err != nil {
		t.Fatalf("new sessions client: %v", err)
	}
	first, err := client.EndSession(t.Context(), "sess_live")
	mu.Lock()
	gotFail := fail
	gotOrder := append([]string(nil), order...)
	gotUpgrades := upgrades
	mu.Unlock()
	if gotFail != "" {
		t.Fatalf("voice socket: %s", gotFail)
	}
	if err != nil {
		t.Fatalf("first end: %v", err)
	}
	if !first.SocketEnded || !first.Deleted {
		t.Fatalf("first end %+v, want the frame and the delete", first)
	}
	if len(gotOrder) != 3 || gotOrder[0] != "resume" || gotOrder[1] != "end" || gotOrder[2] != "delete" {
		t.Fatalf("order %v, want resume, end, delete", gotOrder)
	}
	second, err := client.EndSession(t.Context(), "sess_live")
	if err != nil {
		t.Fatalf("second end: %v", err)
	}
	if !second.SocketEnded || !second.Deleted {
		t.Fatalf("second end %+v, want success without a new socket", second)
	}
	mu.Lock()
	gotUpgrades = upgrades
	mu.Unlock()
	if gotUpgrades != 1 {
		t.Fatalf("upgrades %d, want one socket for both ends", gotUpgrades)
	}
}

// serveVoiceEnd speaks the resume handshake on one hijacked socket.
func serveVoiceEnd(w http.ResponseWriter, r *http.Request, upgrades *int, mu *sync.Mutex, note func(string), setFail func(string)) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		setFail("server cannot hijack")
		return
	}
	conn, rw, err := hj.Hijack()
	if err != nil {
		setFail(err.Error())
		return
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	mu.Lock()
	*upgrades++
	mu.Unlock()
	if r.Header.Get("Authorization") == "" {
		setFail("upgrade missing authorization")
	}
	if _, err := io.WriteString(rw, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n"); err != nil {
		setFail(err.Error())
		return
	}
	if err := rw.Flush(); err != nil {
		setFail(err.Error())
		return
	}
	opcode, payload, err := readWSFrame(rw)
	if err != nil {
		setFail("read resume: " + err.Error())
		return
	}
	var resume voiceResume
	if err := json.Unmarshal(payload, &resume); err != nil || opcode != 1 || resume.Type != "session.resume" || resume.SessionID != "sess_live" {
		setFail("resume frame was not session.resume for sess_live")
		return
	}
	note("resume")
	if err := writeServerText(rw, []byte(`{"type":"session.ready","session_id":"sess_live"}`)); err != nil {
		setFail(err.Error())
		return
	}
	if err := rw.Flush(); err != nil {
		setFail(err.Error())
		return
	}
	opcode, payload, err = readWSFrame(rw)
	if err != nil {
		setFail("read end: " + err.Error())
		return
	}
	if opcode != 1 || string(payload) != string(sessionEndFrame) {
		setFail("end frame was not session.end")
		return
	}
	note("end")
	if err := writeServerText(rw, []byte(`{"type":"session.ended"}`)); err != nil {
		setFail(err.Error())
		return
	}
	_ = rw.Flush()
}

// writeServerText writes one unmasked text frame, the shape the provider
// uses toward the client.
func writeServerText(w io.Writer, payload []byte) error {
	if len(payload) > 125 {
		return errors.New("test frame is too long")
	}
	frame := append([]byte{0x81, byte(len(payload))}, payload...)
	_, err := w.Write(frame)
	return err
}

func TestHoldSocketRejectsEmpty(t *testing.T) {
	client, err := NewSessionsClient("https://agents.example", "test-key", nil)
	if err != nil {
		t.Fatalf("new sessions client: %v", err)
	}
	if err := client.HoldSocket("", &scriptSocket{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty id err %v, want ErrInvalid", err)
	}
	if err := client.HoldSocket("sess", nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("nil socket err %v, want ErrInvalid", err)
	}
}
