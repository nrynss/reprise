package gemini

import (
	"context"
	"fmt"

	"google.golang.org/genai"
)

// GenerateImage draws one square episode panel from the brief and
// returns its PNG bytes. The model id arrives from settings through
// the caller, never from code. A model that answers without image
// bytes reports ErrEmpty, and the caller stores its deterministic
// fallback instead.
func (c *Client) GenerateImage(ctx context.Context, model, prompt string) ([]byte, error) {
	if c == nil || c.generate == nil {
		return nil, fmt.Errorf("gemini: image: %w: missing backend", ErrInvalid)
	}
	if model == "" || prompt == "" {
		return nil, fmt.Errorf("gemini: image: %w: empty model or brief", ErrInvalid)
	}
	contents := []*genai.Content{{
		Role: "user",
		Parts: []*genai.Part{
			genai.NewPartFromText(prompt),
		},
	}}
	resp, err := c.generate(ctx, model, contents, &genai.GenerateContentConfig{
		ResponseModalities: []string{string(genai.ModalityText), string(genai.ModalityImage)},
	})
	if err != nil {
		return nil, fmt.Errorf("gemini: image: %w: %w", ErrGenerate, err)
	}
	if resp == nil {
		return nil, fmt.Errorf("gemini: image: %w: nil response", ErrEmpty)
	}
	for _, candidate := range resp.Candidates {
		if candidate == nil || candidate.Content == nil {
			continue
		}
		for _, part := range candidate.Content.Parts {
			if part == nil || part.InlineData == nil {
				continue
			}
			if len(part.InlineData.Data) == 0 {
				continue
			}
			switch part.InlineData.MIMEType {
			case "image/png", "image/jpeg", "image/webp":
				return append([]byte(nil), part.InlineData.Data...), nil
			}
		}
	}
	return nil, fmt.Errorf("gemini: image: %w: no image bytes", ErrEmpty)
}
