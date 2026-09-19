package gemini

import (
	"context"
	"fmt"

	"google.golang.org/genai"

	"github.com/nrynss/reprise/internal/memory"
)

// markingTemperature keeps the marking deterministic across runs, so
// the same transcript marks the same commitments twice. Quotes must
// match the words exactly, and sampling would drift them.
const markingTemperature = 0.0

// markingMaxTokens caps the marking answers. Candidates point at exact
// quotes rather than explaining them, so the answers stay short.
const markingMaxTokens = 2000

// jsonText sends one transcript prompt and returns the JSON answer
// text with its token usage. The marking calls share this path, and
// the memory package owns the prompts and the parsing on either side.
func (c *Client) jsonText(ctx context.Context, model, prompt string, maxTokens int32) (string, Usage, error) {
	temperature := float32(markingTemperature)
	contents := []*genai.Content{{
		Role: "user",
		Parts: []*genai.Part{
			genai.NewPartFromText(prompt),
		},
	}}
	return c.Generate(ctx, model, contents, &genai.GenerateContentConfig{
		Temperature:      &temperature,
		MaxOutputTokens:  maxTokens,
		ResponseMIMEType: "application/json",
	})
}

// GenerateCommitments lists the things the speaker said they would do
// and returns the answer text with its token usage. The model id
// arrives from settings through the caller, never from code. The
// caller verifies every quote against the episode words.
func (c *Client) GenerateCommitments(ctx context.Context, model, transcript string) (string, Usage, error) {
	if model == "" {
		return "", Usage{}, fmt.Errorf("gemini: commitments: %w: empty model", ErrInvalid)
	}
	if transcript == "" {
		return "", Usage{}, fmt.Errorf("gemini: commitments: %w: empty transcript", ErrInvalid)
	}
	text, usage, err := c.jsonText(ctx, model, memory.CommitmentPrompt(transcript), markingMaxTokens)
	if err != nil {
		return "", Usage{}, err
	}
	return text, usage, nil
}

// GenerateResolution judges whether the later transcript reports doing
// the commitment and returns the answer text with its token usage. The
// model id arrives from settings through the caller, never from code.
// The caller parses the verdict and stores the evidence quote.
func (c *Client) GenerateResolution(ctx context.Context, model, commitment, transcript string) (string, Usage, error) {
	if model == "" {
		return "", Usage{}, fmt.Errorf("gemini: resolution: %w: empty model", ErrInvalid)
	}
	if commitment == "" || transcript == "" {
		return "", Usage{}, fmt.Errorf("gemini: resolution: %w: empty commitment or transcript", ErrInvalid)
	}
	text, usage, err := c.jsonText(ctx, model, memory.ResolutionPrompt(commitment, transcript), markingMaxTokens)
	if err != nil {
		return "", Usage{}, err
	}
	return text, usage, nil
}
