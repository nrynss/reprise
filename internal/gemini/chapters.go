package gemini

import (
	"context"
	"fmt"

	"google.golang.org/genai"
)

// chapterSystem tells the model how to chapter. Chapters mark audible
// shifts, never equal slices. A chapter starts where the ear hears a
// new turn, and every start lands on the transcript clock the caller
// passes.
func chapterSystem() string {
	return "You chapter one personal podcast episode. Read the timed " +
		"transcript and mark where the ear hears a new turn. Answer " +
		"with JSON only, matching the response schema."
}

// chapterPrompt carries one episode transcript with its length and the
// chapter rules. Starts ride in milliseconds on the render clock, and
// the first chapter starts at zero.
func chapterPrompt(transcript string, durationSecs float64, maxTokens int) string {
	return "Chapter the transcript below. The episode runs about " +
		fmt.Sprintf("%.0f seconds. ", durationSecs) +
		"Name at most " + fmt.Sprintf("%d ", maxTokens) +
		"chapters in the answer. The first chapter starts at 0. " +
		"Every chapter lasts at least 10 seconds. Keep at least three " +
		"chapters when the episode allows. Start each chapter where a " +
		"new turn begins, and stamp the start in milliseconds on the " +
		"render clock.\n\nTranscript:\n" + transcript
}

// ChapterSchema validates the chapter answer. The model returns chapter
// drafts with titles and render clock starts, and the caller shapes
// them onto word starts.
func ChapterSchema() *genai.Schema {
	draft := &genai.Schema{
		Type:        genai.TypeObject,
		Description: "One raw chapter with its render clock start.",
		Properties: map[string]*genai.Schema{
			"title":    {Type: genai.TypeString, Description: "Chapter title, specific to this span."},
			"start_ms": {Type: genai.TypeInteger, Description: "Chapter start in milliseconds from the render start."},
		},
		Required: []string{"title", "start_ms"},
	}
	return &genai.Schema{
		Type:        genai.TypeObject,
		Description: "Episode chapters pointing at render clock starts.",
		Properties: map[string]*genai.Schema{
			"chapters": {Type: genai.TypeArray, Description: "Chapters in episode order.", Items: draft},
		},
		Required:         []string{"chapters"},
		PropertyOrdering: []string{"chapters"},
	}
}

// chapterTemperature keeps chapters deterministic across runs, so the
// same render chapters the same way twice.
const chapterTemperature = 0.2

// CompleteChapters asks the named model for episode chapters over the
// rendered transcript and returns the answer text with its token usage.
// The model id arrives from settings through the caller, never from
// code. The caller parses the drafts and shapes them onto word starts.
func (c *Client) CompleteChapters(ctx context.Context, model, transcript string, durationSecs float64, maxTokens int) (string, Usage, error) {
	if model == "" {
		return "", Usage{}, fmt.Errorf("gemini: chapters: %w: empty model", ErrInvalid)
	}
	if transcript == "" {
		return "", Usage{}, fmt.Errorf("gemini: chapters: %w: empty transcript", ErrInvalid)
	}
	if durationSecs <= 0 || maxTokens <= 0 {
		return "", Usage{}, fmt.Errorf("gemini: chapters: %w: duration and token cap must be positive", ErrInvalid)
	}
	temperature := float32(chapterTemperature)
	contents := []*genai.Content{{
		Role: "user",
		Parts: []*genai.Part{
			genai.NewPartFromText(chapterPrompt(transcript, durationSecs, maxTokens)),
		},
	}}
	text, usage, err := c.Generate(ctx, model, contents, &genai.GenerateContentConfig{
		SystemInstruction: genai.NewContentFromText(chapterSystem(), genai.RoleUser),
		Temperature:       &temperature,
		MaxOutputTokens:   int32(maxTokens),
		ResponseMIMEType:  "application/json",
		ResponseSchema:    ChapterSchema(),
	})
	if err != nil {
		return "", Usage{}, err
	}
	return text, usage, nil
}
