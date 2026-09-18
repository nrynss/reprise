package gemini

import (
	"context"
	"fmt"

	"google.golang.org/genai"
)

// DefaultMIME types an Opus stem sent inline. Stems rest as Opus, so the
// editorial pass sends them unchanged.
const DefaultMIME = "audio/ogg"

// spanSchema describes one word range by timeline offsets. Both bounds are
// indexes into the numbered timeline the prompt carries.
func spanSchema(description string) *genai.Schema {
	return &genai.Schema{
		Type:        genai.TypeObject,
		Description: description,
		Properties: map[string]*genai.Schema{
			"start_word": {Type: genai.TypeInteger, Description: "Offset of the first word, from the numbered timeline."},
			"end_word":   {Type: genai.TypeInteger, Description: "Offset of the last word, from the numbered timeline."},
		},
		Required: []string{"start_word", "end_word"},
	}
}

// ResponseSchema validates the editorial answer. The model returns one
// cold open span, zero or more cut spans each with a one line reason, a
// short title, show notes in the speaker's own words, and one callback
// with its quote and the span the quote came from.
func ResponseSchema() *genai.Schema {
	coldOpen := spanSchema("The opening span, 10 to 20 seconds, chosen for delivery.")
	coldOpen.Properties["reason"] = &genai.Schema{Type: genai.TypeString, Description: "One line grounded in how the span sounds."}
	coldOpen.Required = []string{"start_word", "end_word", "reason"}
	cut := spanSchema("One span to remove.")
	cut.Properties["reason"] = &genai.Schema{Type: genai.TypeString, Description: "One line saying why this span goes."}
	cut.Required = []string{"start_word", "end_word", "reason"}
	callback := spanSchema("The span the callback quote came from.")
	callback.Properties["quote"] = &genai.Schema{Type: genai.TypeString, Description: "The speaker's own words, quoted exactly."}
	callback.Properties["text"] = &genai.Schema{Type: genai.TypeString, Description: "The unresolved thing to open next time, in one line."}
	callback.Required = []string{"start_word", "end_word", "quote", "text"}
	return &genai.Schema{
		Type:        genai.TypeObject,
		Description: "Editorial proposals pointing at word offsets in the numbered timeline.",
		Properties: map[string]*genai.Schema{
			"cold_open":  coldOpen,
			"cuts":       {Type: genai.TypeArray, Description: "Spans to remove.", Items: cut},
			"title":      {Type: genai.TypeString, Description: "Short title, specific to this episode."},
			"show_notes": {Type: genai.TypeString, Description: "A few sentences in the speaker's own words."},
			"callback":   callback,
		},
		Required:         []string{"cold_open", "cuts", "title", "show_notes", "callback"},
		PropertyOrdering: []string{"cold_open", "cuts", "title", "show_notes", "callback"},
	}
}

// EditorialRequest carries one editorial pass. Both stems arrive as Opus
// bytes. Timeline numbers every word by its offset, so the answer can
// point at words instead of quoting them.
type EditorialRequest struct {
	// UserStem holds the speaker stem audio.
	UserStem []byte
	// HostStem holds the host stem audio.
	HostStem []byte
	// MIME types both stems. Empty means DefaultMIME.
	MIME string
	// Timeline numbers every word by offset with its timing.
	Timeline string
}

// EditorialAnswer holds one parsed editorial answer with its token usage.
// Usage prices the call for the budget settle.
type EditorialAnswer struct {
	// JSON is the raw answer text, kept for the receipt store.
	JSON string
	// Usage counts the call in tokens.
	Usage Usage
}

// editorialTemperature keeps the proposals deterministic across runs, so
// the same session proposes the same episode twice.
const editorialTemperature = 0.2

// editorialMaxTokens caps the answer length. Proposals point at offsets
// rather than quoting audio, so the answer stays short.
const editorialMaxTokens = 4000

// GenerateEditorial hears both stems, reads the word timeline, and returns
// the proposals as JSON validated against ResponseSchema. The model id
// arrives from settings through the caller, never from code.
func (c *Client) GenerateEditorial(ctx context.Context, model string, req EditorialRequest) (EditorialAnswer, error) {
	if model == "" {
		return EditorialAnswer{}, fmt.Errorf("gemini: editorial: %w: empty model", ErrInvalid)
	}
	if len(req.UserStem) == 0 || len(req.HostStem) == 0 {
		return EditorialAnswer{}, fmt.Errorf("gemini: editorial: %w: empty stem", ErrInvalid)
	}
	if req.Timeline == "" {
		return EditorialAnswer{}, fmt.Errorf("gemini: editorial: %w: empty timeline", ErrInvalid)
	}
	mime := req.MIME
	if mime == "" {
		mime = DefaultMIME
	}
	temperature := float32(editorialTemperature)
	contents := []*genai.Content{{
		Role: "user",
		Parts: []*genai.Part{
			genai.NewPartFromText(editorialPrompt(req.Timeline)),
			genai.NewPartFromBytes(req.UserStem, mime),
			genai.NewPartFromBytes(req.HostStem, mime),
		},
	}}
	text, usage, err := c.Generate(ctx, model, contents, &genai.GenerateContentConfig{
		SystemInstruction: genai.NewContentFromText(editorialSystem(), genai.RoleUser),
		Temperature:       &temperature,
		MaxOutputTokens:   editorialMaxTokens,
		ResponseMIMEType:  "application/json",
		ResponseSchema:    ResponseSchema(),
	})
	if err != nil {
		return EditorialAnswer{}, err
	}
	return EditorialAnswer{JSON: text, Usage: usage}, nil
}
