package mail

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

// DefaultBaseURL is the Resend API origin. A config without a base URL
// uses it. Tests point the base URL at a local server instead.
const DefaultBaseURL = "https://api.resend.com"

// ErrInvalid reports a message the client refuses without calling the
// provider. The recipient, the subject, or the body is missing.
var ErrInvalid = errors.New("mail: invalid message")

// ErrRejected reports a provider refusal. The request reached Resend and
// came back with a client error, so resending the same message fails again.
var ErrRejected = errors.New("mail: rejected")

// ErrUnavailable reports a provider or network failure. The message may
// never have sent, so the caller decides whether to retry.
var ErrUnavailable = errors.New("mail: unavailable")

// Config carries one client construction. The key, the sender, and the
// reply address arrive from settings at boot. The package never reads
// the environment itself.
type Config struct {
	// APIKey is the Resend secret. It travels as a bearer token.
	APIKey string
	// From is the sender the provider shows, such as a named address.
	From string
	// ReplyTo is optional. Replies go here when set, else to From.
	ReplyTo string
	// BaseURL overrides the API origin. Tests use a local server.
	BaseURL string
	// HTTP overrides the transport. It defaults to the shared client.
	HTTP *http.Client
}

// Message carries one send. To holds one recipient address. Text and HTML
// carry the body. At least one body form must be set.
type Message struct {
	// To is the recipient address.
	To string
	// Subject is the message subject.
	Subject string
	// Text is the plain text body.
	Text string
	// HTML is the HTML body.
	HTML string
}

// SendResult carries the provider answer. ID is the provider message id.
type SendResult struct {
	// ID is the provider message id.
	ID string
}

// Sender sends one message. Consumers declare this interface where they
// use it. Client and Fake both implement it.
type Sender interface {
	// Send delivers one message and returns the provider message id.
	Send(ctx context.Context, msg Message) (SendResult, error)
}

// Client sends mail through Resend. Build it with New. The zero value
// sends nothing. Every send fails validation first.
type Client struct {
	apiKey  string
	from    string
	replyTo string
	baseURL string
	http    *http.Client
}

// New builds a Client from cfg. It fails when the key or the sender is
// empty. An empty base URL selects the production origin. A nil transport
// selects the shared client.
func New(cfg Config) (*Client, error) {
	if strings.TrimSpace(cfg.APIKey) == "" || strings.TrimSpace(cfg.From) == "" {
		return nil, fmt.Errorf("mail: new client: %w: key and sender are required", ErrInvalid)
	}
	base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if base == "" {
		base = DefaultBaseURL
	}
	transport := cfg.HTTP
	if transport == nil {
		transport = http.DefaultClient
	}
	return &Client{
		apiKey:  cfg.APIKey,
		from:    cfg.From,
		replyTo: strings.TrimSpace(cfg.ReplyTo),
		baseURL: base,
		http:    transport,
	}, nil
}

// sendRequest is the wire body for one send. Empty body forms and an
// unset reply address stay out of the JSON.
type sendRequest struct {
	From    string   `json:"from"`
	To      []string `json:"to"`
	Subject string   `json:"subject"`
	Text    string   `json:"text,omitempty"`
	HTML    string   `json:"html,omitempty"`
	ReplyTo string   `json:"reply_to,omitempty"`
}

// sendResponse is the wire answer for a send.
type sendResponse struct {
	ID string `json:"id"`
}

// Send delivers one message through Resend and returns the provider
// message id. It validates the message before any network call. A 4xx
// answer wraps ErrRejected. A 5xx or transport failure wraps
// ErrUnavailable. The error never carries the provider message text.
func (c *Client) Send(ctx context.Context, msg Message) (SendResult, error) {
	if c == nil || c.apiKey == "" || c.from == "" {
		return SendResult{}, fmt.Errorf("mail: send: %w: client is not built", ErrInvalid)
	}
	if strings.TrimSpace(msg.To) == "" || strings.TrimSpace(msg.Subject) == "" {
		return SendResult{}, fmt.Errorf("mail: send: %w: recipient and subject are required", ErrInvalid)
	}
	if msg.Text == "" && msg.HTML == "" {
		return SendResult{}, fmt.Errorf("mail: send: %w: text or HTML is required", ErrInvalid)
	}
	body, err := json.Marshal(sendRequest{
		From:    c.from,
		To:      []string{msg.To},
		Subject: msg.Subject,
		Text:    msg.Text,
		HTML:    msg.HTML,
		ReplyTo: c.replyTo,
	})
	if err != nil {
		return SendResult{}, fmt.Errorf("mail: send: %w: encode: %v", ErrInvalid, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/emails", bytes.NewReader(body))
	if err != nil {
		return SendResult{}, fmt.Errorf("mail: send: %w: build request: %v", ErrInvalid, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return SendResult{}, fmt.Errorf("mail: send: %w: transport: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return SendResult{}, fmt.Errorf("mail: send: %w: read answer: %v", ErrUnavailable, err)
	}
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		var out sendResponse
		if err := json.Unmarshal(raw, &out); err != nil || strings.TrimSpace(out.ID) == "" {
			return SendResult{}, fmt.Errorf("mail: send: %w: bad success answer with status %d", ErrUnavailable, resp.StatusCode)
		}
		return SendResult(out), nil
	case resp.StatusCode >= 400 && resp.StatusCode < 500:
		return SendResult{}, fmt.Errorf("mail: send: %w: status %d", ErrRejected, resp.StatusCode)
	default:
		return SendResult{}, fmt.Errorf("mail: send: %w: status %d", ErrUnavailable, resp.StatusCode)
	}
}

// Fake records messages for tests. It implements Sender. The zero value
// records and answers an empty id.
type Fake struct {
	// Mu guards every field below.
	Mu sync.Mutex
	// Sent holds every recorded message in arrival order.
	Sent []Message
	// ID is the scripted provider id each send returns.
	ID string
	// Err is the scripted failure each send returns.
	Err error
}

// Send records msg and returns the scripted answer. A scripted error
// still records the message, so a test can inspect refused attempts.
func (f *Fake) Send(ctx context.Context, msg Message) (SendResult, error) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	f.Sent = append(f.Sent, msg)
	if f.Err != nil {
		return SendResult{}, f.Err
	}
	return SendResult{ID: f.ID}, nil
}

// Messages returns a copy of the recorded messages in arrival order.
func (f *Fake) Messages() []Message {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	return append([]Message(nil), f.Sent...)
}
