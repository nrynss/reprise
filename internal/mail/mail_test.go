package mail

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// capturedSend is the test view of one wire body. ReplyTo is a pointer,
// so the test tells an absent field apart from an empty one.
type capturedSend struct {
	From    string   `json:"from"`
	To      []string `json:"to"`
	Subject string   `json:"subject"`
	Text    string   `json:"text"`
	HTML    string   `json:"html"`
	ReplyTo *string  `json:"reply_to"`
}

// captureServer records one request and answers a scripted status and
// body. It keeps the headers the client sent.
type captureServer struct {
	t      *testing.T
	mu     sync.Mutex
	method string
	path   string
	auth   string
	ctype  string
	raw    []byte
	status int
	body   string
}

// newCaptureServer starts a server that answers status and body for one
// send. The test reads the recorded request after the send returns.
func newCaptureServer(t *testing.T, status int, body string) (*httptest.Server, *captureServer) {
	t.Helper()
	cap := &captureServer{t: t, status: status, body: body}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		cap.mu.Lock()
		cap.method = r.Method
		cap.path = r.URL.Path
		cap.auth = r.Header.Get("Authorization")
		cap.ctype = r.Header.Get("Content-Type")
		cap.raw = raw
		cap.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, cap
}

// decoded returns the recorded wire body. It fails the test on bad JSON.
func (c *captureServer) decoded(t *testing.T) capturedSend {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	var out capturedSend
	if err := json.Unmarshal(c.raw, &out); err != nil {
		t.Fatalf("decode request body %q: %v", c.raw, err)
	}
	return out
}

// testMessage returns a valid message every shape test starts from.
func testMessage() Message {
	return Message{To: "reader@example.com", Subject: "Your sign-in code", Text: "Your code is 123456."}
}

// testClient builds a client against the given server. ReplyTo stays
// empty unless the caller sets it after.
func testClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	client, err := New(Config{APIKey: "test-key", From: "Reprise <reprise@mail.example.com>", BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	return client
}

// TestSendRequestShape pins the wire shape: method, path, sender,
// recipient list, subject, text body, and the bearer header.
func TestSendRequestShape(t *testing.T) {
	srv, cap := newCaptureServer(t, http.StatusOK, `{"id":"msg-shape-1"}`)
	client := testClient(t, srv)
	got, err := client.Send(context.Background(), testMessage())
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if got.ID != "msg-shape-1" {
		t.Fatalf("result id %q, want %q", got.ID, "msg-shape-1")
	}
	cap.mu.Lock()
	defer cap.mu.Unlock()
	if cap.method != http.MethodPost {
		t.Errorf("method %q, want POST", cap.method)
	}
	if cap.path != "/emails" {
		t.Errorf("path %q, want /emails", cap.path)
	}
	if cap.auth != "Bearer test-key" {
		t.Errorf("authorization %q, want bearer test-key", cap.auth)
	}
	if cap.ctype != "application/json" {
		t.Errorf("content type %q, want application/json", cap.ctype)
	}
	var out capturedSend
	if err := json.Unmarshal(cap.raw, &out); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	if out.From != "Reprise <reprise@mail.example.com>" {
		t.Errorf("from %q, want the configured sender", out.From)
	}
	if len(out.To) != 1 || out.To[0] != "reader@example.com" {
		t.Errorf("to %v, want one recipient", out.To)
	}
	if out.Subject != "Your sign-in code" {
		t.Errorf("subject %q, want the message subject", out.Subject)
	}
	if out.Text != "Your code is 123456." {
		t.Errorf("text %q, want the message text", out.Text)
	}
	if out.HTML != "" {
		t.Errorf("html %q, want it empty", out.HTML)
	}
	if out.ReplyTo != nil {
		t.Errorf("reply_to %q, want it absent when unset", *out.ReplyTo)
	}
}

// TestSendOmitsEmptyBodyForms checks an empty body form stays out of the
// JSON. A text-only message carries no HTML key, and an HTML-only
// message carries no text key.
func TestSendOmitsEmptyBodyForms(t *testing.T) {
	srv, cap := newCaptureServer(t, http.StatusOK, `{"id":"msg-forms-1"}`)
	client := testClient(t, srv)
	// bodyKeys is the test view of the raw wire body. A nil pointer
	// means the key was absent, which a plain string cannot show.
	type bodyKeys struct {
		Text *string `json:"text"`
		HTML *string `json:"html"`
	}
	rawKeys := func() bodyKeys {
		t.Helper()
		cap.mu.Lock()
		defer cap.mu.Unlock()
		var keys bodyKeys
		if err := json.Unmarshal(cap.raw, &keys); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		return keys
	}
	if _, err := client.Send(context.Background(), testMessage()); err != nil {
		t.Fatalf("send text: %v", err)
	}
	if keys := rawKeys(); keys.Text == nil || keys.HTML != nil {
		t.Errorf("text-only body: text present %v, html present %v", keys.Text != nil, keys.HTML != nil)
	}
	htmlOnly := testMessage()
	htmlOnly.Text = ""
	htmlOnly.HTML = "<p>Your code is <b>123456</b>.</p>"
	if _, err := client.Send(context.Background(), htmlOnly); err != nil {
		t.Fatalf("send html: %v", err)
	}
	got := cap.decoded(t)
	if got.HTML != htmlOnly.HTML {
		t.Errorf("html %q, want the message html", got.HTML)
	}
	if keys := rawKeys(); keys.Text != nil || keys.HTML == nil {
		t.Errorf("html-only body: text present %v, html present %v", keys.Text != nil, keys.HTML != nil)
	}
}

// TestSendIncludesReplyToWhenSet checks the configured reply address
// reaches the wire only when the config sets it.
func TestSendIncludesReplyToWhenSet(t *testing.T) {
	srv, cap := newCaptureServer(t, http.StatusOK, `{"id":"msg-reply-1"}`)
	client, err := New(Config{
		APIKey:  "test-key",
		From:    "Reprise <reprise@mail.example.com>",
		ReplyTo: "owner@example.com",
		BaseURL: srv.URL,
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	if _, err := client.Send(context.Background(), testMessage()); err != nil {
		t.Fatalf("send: %v", err)
	}
	got := cap.decoded(t)
	if got.ReplyTo == nil || *got.ReplyTo != "owner@example.com" {
		t.Errorf("reply_to %v, want owner@example.com", got.ReplyTo)
	}
}

// TestNewDefaultsBaseURL checks an empty base URL selects the
// production origin.
func TestNewDefaultsBaseURL(t *testing.T) {
	client, err := New(Config{APIKey: "test-key", From: "Reprise <reprise@mail.example.com>"})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	if client.baseURL != DefaultBaseURL {
		t.Errorf("base url %q, want %q", client.baseURL, DefaultBaseURL)
	}
	if client.http != http.DefaultClient {
		t.Error("transport is not the shared client")
	}
}

// TestNewInvalid checks an empty key or sender fails before any send.
func TestNewInvalid(t *testing.T) {
	for _, cfg := range []Config{
		{From: "Reprise <reprise@mail.example.com>"},
		{APIKey: "test-key"},
		{},
	} {
		if _, err := New(cfg); !errors.Is(err, ErrInvalid) {
			t.Errorf("new client with %+v: error %v, want ErrInvalid", cfg, err)
		}
	}
}

// TestSendInvalid checks a bad message fails without touching the
// network. The server counts calls to prove it stayed silent.
func TestSendInvalid(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"id":"msg-never-1"}`)
	}))
	t.Cleanup(srv.Close)
	client := testClient(t, srv)
	valid := testMessage()
	cases := []Message{
		{Subject: valid.Subject, Text: valid.Text},
		{To: valid.To, Text: valid.Text},
		{To: valid.To, Subject: valid.Subject},
		{To: "  ", Subject: valid.Subject, Text: valid.Text},
	}
	for i, msg := range cases {
		if _, err := client.Send(context.Background(), msg); !errors.Is(err, ErrInvalid) {
			t.Errorf("case %d: error %v, want ErrInvalid", i, err)
		}
	}
	if calls != 0 {
		t.Errorf("server saw %d calls, want none", calls)
	}
}

// TestSendRejected checks every 4xx status wraps ErrRejected. The body
// carries a provider-shaped error the client must never match on.
func TestSendRejected(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusUnprocessableEntity, http.StatusTooManyRequests} {
		srv, _ := newCaptureServer(t, status, `{"error":{"message":"validation failed"}}`)
		client := testClient(t, srv)
		_, err := client.Send(context.Background(), testMessage())
		if !errors.Is(err, ErrRejected) {
			t.Errorf("status %d: error %v, want ErrRejected", status, err)
		}
		if errors.Is(err, ErrInvalid) || errors.Is(err, ErrUnavailable) {
			t.Errorf("status %d: error %v matches a second sentinel", status, err)
		}
		if err != nil && strings.Contains(err.Error(), "validation failed") {
			t.Errorf("status %d: error repeats the provider text", status)
		}
	}
}

// TestSendUnavailable checks every 5xx status wraps ErrUnavailable.
func TestSendUnavailable(t *testing.T) {
	for _, status := range []int{http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable} {
		srv, _ := newCaptureServer(t, status, `{"error":{"message":"try again"}}`)
		client := testClient(t, srv)
		_, err := client.Send(context.Background(), testMessage())
		if !errors.Is(err, ErrUnavailable) {
			t.Errorf("status %d: error %v, want ErrUnavailable", status, err)
		}
	}
}

// TestSendTransportFailure checks a dead server wraps ErrUnavailable.
func TestSendTransportFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	url := srv.URL
	srv.Close()
	client, err := New(Config{APIKey: "test-key", From: "Reprise <reprise@mail.example.com>", BaseURL: url})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	if _, err := client.Send(context.Background(), testMessage()); !errors.Is(err, ErrUnavailable) {
		t.Errorf("dead server: error %v, want ErrUnavailable", err)
	}
}

// TestSendBadSuccessBody checks a 2xx answer without a message id
// wraps ErrUnavailable instead of returning an empty result.
func TestSendBadSuccessBody(t *testing.T) {
	for _, body := range []string{`{}`, `{"id":""}`, `not json`} {
		srv, _ := newCaptureServer(t, http.StatusOK, body)
		client := testClient(t, srv)
		got, err := client.Send(context.Background(), testMessage())
		if !errors.Is(err, ErrUnavailable) {
			t.Errorf("body %q: error %v, want ErrUnavailable", body, err)
		}
		if got.ID != "" {
			t.Errorf("body %q: result id %q, want empty", body, got.ID)
		}
	}
}

// TestFakeRecords checks the fake keeps every message in order and
// returns the scripted id.
func TestFakeRecords(t *testing.T) {
	fake := &Fake{ID: "fake-id-1"}
	var _ Sender = fake
	first := testMessage()
	second := testMessage()
	second.To = "other@example.com"
	if _, err := fake.Send(context.Background(), first); err != nil {
		t.Fatalf("fake send first: %v", err)
	}
	got, err := fake.Send(context.Background(), second)
	if err != nil {
		t.Fatalf("fake send second: %v", err)
	}
	if got.ID != "fake-id-1" {
		t.Fatalf("fake result id %q, want fake-id-1", got.ID)
	}
	sent := fake.Messages()
	if len(sent) != 2 || sent[0] != first || sent[1] != second {
		t.Fatalf("fake recorded %+v, want both messages in order", sent)
	}
}

// TestFakeError checks a scripted failure still records the message.
func TestFakeError(t *testing.T) {
	fake := &Fake{Err: ErrRejected}
	if _, err := fake.Send(context.Background(), testMessage()); !errors.Is(err, ErrRejected) {
		t.Fatalf("fake send: error %v, want ErrRejected", err)
	}
	if len(fake.Messages()) != 1 {
		t.Fatalf("fake recorded %d messages, want one", len(fake.Messages()))
	}
}
