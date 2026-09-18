package gemini

import (
	"context"
	"errors"
	"fmt"

	"cloud.google.com/go/auth/credentials"
	"google.golang.org/genai"
)

// ErrInvalid reports a client call with an empty project, location, model,
// credential, or audio part.
var ErrInvalid = errors.New("gemini: invalid argument")

// ErrGenerate reports a model call that returned no usable text. The wrapped
// error carries the cause.
var ErrGenerate = errors.New("gemini: generate failed")

// ErrEmpty reports a model answer with no candidates or no text. The caller
// treats it like any other model failure and falls back.
var ErrEmpty = errors.New("gemini: empty answer")

// Config carries one client construction. Project and Location arrive from
// settings as ordinary values. CredentialJSON is the service account key
// file contents, resolved through the file source at boot.
type Config struct {
	// Project names the Google Cloud project that bills Vertex AI.
	Project string
	// Location names the Vertex AI region the client calls.
	Location string
	// CredentialJSON holds the service account key JSON.
	CredentialJSON string
}

// GenerateFunc calls the model. The production client binds it to the
// Vertex backend. Tests bind it to a scripted answer, so no test needs a
// key, a network, or a microphone.
type GenerateFunc func(ctx context.Context, model string, contents []*genai.Content, config *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error)

// Client talks to one Vertex backend. Build it with NewClient and use it
// from the server only. The zero value has no backend.
type Client struct {
	generate GenerateFunc
}

// NewClient opens a Vertex client on the project and location from
// settings, authenticated with the service account key. It fails when the
// project, the location, or the key is empty, and when the key is not a
// service account document.
func NewClient(ctx context.Context, cfg Config) (*Client, error) {
	if cfg.Project == "" || cfg.Location == "" || cfg.CredentialJSON == "" {
		return nil, fmt.Errorf("gemini: new client: %w: empty project, location, or credential", ErrInvalid)
	}
	creds, err := credentials.NewCredentialsFromJSON(
		credentials.ServiceAccount,
		[]byte(cfg.CredentialJSON),
		&credentials.DetectOptions{Scopes: []string{"https://www.googleapis.com/auth/cloud-platform"}},
	)
	if err != nil {
		return nil, fmt.Errorf("gemini: new client: %w", err)
	}
	backend, err := genai.NewClient(ctx, &genai.ClientConfig{
		Project:     cfg.Project,
		Location:    cfg.Location,
		Backend:     genai.BackendVertexAI,
		Credentials: creds,
	})
	if err != nil {
		return nil, fmt.Errorf("gemini: new client: %w", err)
	}
	return &Client{generate: backend.Models.GenerateContent}, nil
}

// NewTestClient binds the client to a scripted answer. Tests use it to run
// the editorial pass offline with no key and no network.
func NewTestClient(fn GenerateFunc) *Client {
	return &Client{generate: fn}
}

// Usage counts one model answer in tokens, broken down the way the
// provider reports it. The caller prices the call from these counts.
type Usage struct {
	// Prompt counts the input tokens, audio included.
	Prompt int64
	// Candidates counts the output tokens.
	Candidates int64
	// Total counts every token the call billed.
	Total int64
}

// Generate sends contents to the named model and returns the answer text
// with its token usage. An empty model, empty contents, or a nil answer
// fails. The model id always arrives from settings through the caller.
func (c *Client) Generate(ctx context.Context, model string, contents []*genai.Content, config *genai.GenerateContentConfig) (string, Usage, error) {
	if c == nil || c.generate == nil {
		return "", Usage{}, fmt.Errorf("gemini: generate: %w: missing backend", ErrInvalid)
	}
	if model == "" || len(contents) == 0 {
		return "", Usage{}, fmt.Errorf("gemini: generate: %w: empty model or contents", ErrInvalid)
	}
	resp, err := c.generate(ctx, model, contents, config)
	if err != nil {
		return "", Usage{}, fmt.Errorf("gemini: generate: %w: %w", ErrGenerate, err)
	}
	if resp == nil {
		return "", Usage{}, fmt.Errorf("gemini: generate: %w: nil response", ErrEmpty)
	}
	text := resp.Text()
	if text == "" {
		return "", Usage{}, fmt.Errorf("gemini: generate: %w: no text", ErrEmpty)
	}
	var usage Usage
	if meta := resp.UsageMetadata; meta != nil {
		usage = Usage{Prompt: int64(meta.PromptTokenCount), Candidates: int64(meta.CandidatesTokenCount), Total: int64(meta.TotalTokenCount)}
	}
	return text, usage, nil
}
