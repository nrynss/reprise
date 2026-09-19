package analysis

import (
	"context"
	"io"
	"time"
)

// Word is one transcribed word with millisecond offsets on the rendered
// file. The render starts at zero, so these offsets are the episode
// clock the chapters and mentions share.
type Word struct {
	// Text is the word as heard, with its original casing.
	Text string
	// StartMs is the word start in milliseconds from the render start.
	StartMs int64
	// EndMs is the word end in milliseconds from the render start.
	EndMs int64
	// Confidence scores the word from zero to one.
	Confidence float64
}

// Entity is one detected name, place, or time with its span on the
// render clock.
type Entity struct {
	// Type names the entity kind the provider returned, such as
	// person_name or location.
	Type string
	// Text is the entity wording as heard.
	Text string
	// StartMs is the span start in milliseconds from the render start.
	StartMs int64
	// EndMs is the span end in milliseconds from the render start.
	EndMs int64
}

// Span is one occurrence window on the render clock.
type Span struct {
	// StartMs is the occurrence start in milliseconds.
	StartMs int64
	// EndMs is the occurrence end in milliseconds.
	EndMs int64
}

// KeyPhrase is one recurring phrase with every span where it occurs.
type KeyPhrase struct {
	// Text is the phrase wording as heard.
	Text string
	// Rank scores the phrase importance from zero upward.
	Rank float64
	// Count carries how often the phrase occurs.
	Count int
	// Spans holds one window per occurrence on the render clock.
	Spans []Span
}

// Summary carries the episode notes from the same provider request.
type Summary struct {
	// Headline is the one line summary.
	Headline string
	// Bullets holds the short summary points.
	Bullets []string
	// Block is the running text summary.
	Block string
}

// TranscriptResult is one finished batch transcription with every
// feature the single creation call asked for. Raw carries the exact
// provider bytes, so the caller persists the response before deleting
// the provider copy.
type TranscriptResult struct {
	// ID is the provider transcript id.
	ID string
	// Text is the full transcript text, or the deletion marker.
	Text string
	// Words holds every word with its timing, or nothing after deletion.
	Words []Word
	// Entities holds every detected entity, or nothing after deletion.
	Entities []Entity
	// Phrases holds every key phrase, or nothing after deletion.
	Phrases []KeyPhrase
	// Summary carries the episode notes from the same request.
	Summary Summary
	// AudioDurationSecs is the billed audio length in seconds.
	AudioDurationSecs int64
	// Raw is the full provider response body for the receipt store.
	Raw []byte
}

// CreateRequest starts one batch transcription of an uploaded render.
// One call carries words, entities, key phrases, and the summary
// together, so the run asserts exactly one creation call per episode.
type CreateRequest struct {
	// AudioURL is the upload URL the upload call returned.
	AudioURL string
}

// Transcriber carries the batch exchange for one render. The provider
// adapter implements it in production, where one creation call asks for
// every feature at once. Tests bind the scripted double, which answers
// all four features from that single call.
type Transcriber interface {
	// Upload posts render bytes and returns the URL the create call reads.
	Upload(ctx context.Context, audio io.Reader) (string, error)
	// Create starts one transcription and returns its id.
	Create(ctx context.Context, req CreateRequest) (string, error)
	// Wait polls one transcript until it completes or fails.
	Wait(ctx context.Context, id string, interval time.Duration) (TranscriptResult, error)
	// Get fetches one transcript with its receipt bytes.
	Get(ctx context.Context, id string) (TranscriptResult, error)
	// Delete removes one transcript by id.
	Delete(ctx context.Context, id string) error
}

// Uploader is the transport subset the batch client already serves. The
// compat test pins a real client against it, so the seam cannot drift
// from the adapter it will bind in production.
type Uploader interface {
	// Upload posts render bytes and returns the URL the create call reads.
	Upload(ctx context.Context, audio io.Reader) (string, error)
}

// Segment is one timed slice of transcript text for the chapter prompt.
type Segment struct {
	// StartMs is the segment start in milliseconds from the render start.
	StartMs int64
	// Text is the segment wording as heard.
	Text string
}

// ChapterRequest carries one chapter call over the rendered transcript.
type ChapterRequest struct {
	// Transcript is the timed transcript text the model chapters.
	Transcript string
	// DurationSecs is the render length in seconds.
	DurationSecs float64
	// MaxTokens caps the chapter answer length.
	MaxTokens int
}

// ChapterDraft is one raw chapter the model returned, before shaping.
type ChapterDraft struct {
	// Title is the chapter title as returned.
	Title string
	// StartMs is the chapter start in milliseconds from the render start.
	StartMs int64
}

// Usage counts the tokens one chapter call spent.
type Usage struct {
	// PromptTokens counts the tokens the request carried.
	PromptTokens int64
	// CompletionTokens counts the tokens the answer carried.
	CompletionTokens int64
}

// ChapterAnswer carries one chapter call result. Raw holds the exact
// provider bytes for the receipt store.
type ChapterAnswer struct {
	// Drafts holds the raw chapters before shaping.
	Drafts []ChapterDraft
	// Usage counts the tokens the call spent.
	Usage Usage
	// Raw is the full provider response body for the receipt store.
	Raw []byte
}

// Chapterer carries the chapter call over the rendered transcript. The
// provider adapter implements it against the chapter endpoint in
// production. Tests bind the scripted double, so shaping runs offline.
type Chapterer interface {
	// CompleteChapters asks the named model for episode chapters.
	CompleteChapters(ctx context.Context, model string, req ChapterRequest) (ChapterAnswer, error)
}

// Chapter is one shaped episode chapter with its start on the episode
// clock.
type Chapter struct {
	// Title is the chapter title.
	Title string
	// StartMs is the chapter start in milliseconds from the render start.
	StartMs int64
}

// Rates prices chapter tokens in US dollars per million tokens. The
// wiring fills them from the model catalog beside the chapter model id,
// so no price lives in code.
type Rates struct {
	// PromptPerMillionUSD prices one million prompt tokens.
	PromptPerMillionUSD float64
	// CompletionPerMillionUSD prices one million completion tokens.
	CompletionPerMillionUSD float64
}
