package gemini_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"google.golang.org/genai"

	"github.com/nrynss/reprise/internal/gemini"
)

// TestCompleteChaptersSendsTranscript checks one chapter call carries
// the transcript with the JSON schema attached, and the settings model
// passes through untouched.
func TestCompleteChaptersSendsTranscript(t *testing.T) {
	t.Parallel()
	var gotModel string
	var gotConfig *genai.GenerateContentConfig
	client := gemini.NewTestClient(func(_ context.Context, model string, _ []*genai.Content, config *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
		gotModel, gotConfig = model, config
		return answer(`{"chapters":[{"title":"Dawn Ferry","start_ms":0}]}`), nil
	})
	text, usage, err := client.CompleteChapters(t.Context(), "flash", "[00:00] the ferry leaves", 600, 800)
	if err != nil {
		t.Fatalf("chapters: %v", err)
	}
	if !strings.Contains(text, "Dawn Ferry") {
		t.Fatalf("answer = %q, want the scripted chapters", text)
	}
	if gotModel != "flash" {
		t.Fatalf("model = %q, want the settings model passed through", gotModel)
	}
	if usage.Total != 17 {
		t.Fatalf("usage total = %d, want 17", usage.Total)
	}
	if gotConfig == nil || gotConfig.ResponseMIMEType != "application/json" || gotConfig.ResponseSchema == nil {
		t.Fatalf("config = %+v, want the JSON schema attached", gotConfig)
	}
	if _, ok := gotConfig.ResponseSchema.Properties["chapters"]; !ok {
		t.Fatal("schema misses the chapters array")
	}
}

// TestCompleteChaptersRefusesEmpty checks an empty transcript or model
// never reaches the backend, so a programming error cannot spend.
func TestCompleteChaptersRefusesEmpty(t *testing.T) {
	t.Parallel()
	calls := 0
	client := gemini.NewTestClient(func(context.Context, string, []*genai.Content, *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
		calls++
		return answer(`{}`), nil
	})
	for _, tc := range []struct {
		name       string
		model      string
		transcript string
	}{
		{"model", "", "words"},
		{"transcript", "flash", ""},
	} {
		if _, _, err := client.CompleteChapters(t.Context(), tc.model, tc.transcript, 600, 800); !errors.Is(err, gemini.ErrInvalid) {
			t.Fatalf("%s: error = %v, want ErrInvalid", tc.name, err)
		}
	}
	if calls != 0 {
		t.Fatalf("backend calls = %d, want none for empty input", calls)
	}
}

// imageAnswer builds a canned image response with PNG bytes behind an
// inline part, the way the backend returns a drawn panel.
func imageAnswer(png []byte) *genai.GenerateContentResponse {
	return &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{{
			Content: &genai.Content{Role: "model", Parts: []*genai.Part{{
				Text:       "Here is the panel.",
				InlineData: &genai.Blob{MIMEType: "image/png", Data: png},
			}}},
			FinishReason: genai.FinishReasonStop,
		}},
	}
}

// TestGenerateImageReturnsBytes checks one image call asks for the
// image modality and returns the panel bytes behind the inline part.
func TestGenerateImageReturnsBytes(t *testing.T) {
	t.Parallel()
	var gotModel string
	var gotConfig *genai.GenerateContentConfig
	want := []byte{0x89, 0x50, 0x4e, 0x47}
	client := gemini.NewTestClient(func(_ context.Context, model string, _ []*genai.Content, config *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
		gotModel, gotConfig = model, config
		return imageAnswer(want), nil
	})
	got, err := client.GenerateImage(t.Context(), "flash", "a harbor at dawn, no faces, no text")
	if err != nil {
		t.Fatalf("image: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("image bytes = %x, want the scripted panel", got)
	}
	if gotModel != "flash" {
		t.Fatalf("model = %q, want the settings model passed through", gotModel)
	}
	modalities := append([]string(nil), gotConfig.ResponseModalities...)
	found := false
	for _, modality := range modalities {
		if modality == "IMAGE" {
			found = true
		}
	}
	if !found {
		t.Fatalf("modalities = %v, want IMAGE among them", modalities)
	}
}

// TestGenerateImageFallsBackWithoutBytes checks a text only answer
// fails with the empty sentinel, so the caller stores its fallback.
func TestGenerateImageFallsBackWithoutBytes(t *testing.T) {
	t.Parallel()
	client := gemini.NewTestClient(func(context.Context, string, []*genai.Content, *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
		return answer("I cannot draw that."), nil
	})
	if _, err := client.GenerateImage(t.Context(), "flash", "a harbor"); !errors.Is(err, gemini.ErrEmpty) {
		t.Fatalf("error = %v, want ErrEmpty", err)
	}
	if _, err := client.GenerateImage(t.Context(), "", "a harbor"); !errors.Is(err, gemini.ErrInvalid) {
		t.Fatalf("empty model error = %v, want ErrInvalid", err)
	}
}

// TestGenerateCommitmentsSendsTranscript checks one marking call
// carries the transcript and returns the JSON the verifier parses.
func TestGenerateCommitmentsSendsTranscript(t *testing.T) {
	t.Parallel()
	var gotContents []*genai.Content
	client := gemini.NewTestClient(func(_ context.Context, _ string, contents []*genai.Content, _ *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
		gotContents = contents
		return answer(`{"commitments":[{"quote":"I will call Mara","text":"call Mara"}]}`), nil
	})
	text, _, err := client.GenerateCommitments(t.Context(), "flash", "I will call Mara tomorrow")
	if err != nil {
		t.Fatalf("commitments: %v", err)
	}
	if !strings.Contains(text, "call Mara") {
		t.Fatalf("answer = %q, want the scripted marking", text)
	}
	if len(gotContents) != 1 || !strings.Contains(gotContents[0].Parts[0].Text, "I will call Mara tomorrow") {
		t.Fatalf("prompt misses the transcript: %+v", gotContents)
	}
	if _, _, err := client.GenerateCommitments(t.Context(), "flash", ""); !errors.Is(err, gemini.ErrInvalid) {
		t.Fatalf("empty transcript error = %v, want ErrInvalid", err)
	}
}

// TestGenerateResolutionJudgesDoing checks one resolution call carries
// both the commitment and the later transcript.
func TestGenerateResolutionJudgesDoing(t *testing.T) {
	t.Parallel()
	var gotContents []*genai.Content
	client := gemini.NewTestClient(func(_ context.Context, _ string, contents []*genai.Content, _ *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
		gotContents = contents
		return answer(`{"done":true,"quote":"I called Mara"}`), nil
	})
	text, _, err := client.GenerateResolution(t.Context(), "flash", "call Mara", "I called Mara today")
	if err != nil {
		t.Fatalf("resolution: %v", err)
	}
	if !strings.Contains(text, "I called Mara") {
		t.Fatalf("answer = %q, want the scripted verdict", text)
	}
	prompt := gotContents[0].Parts[0].Text
	if !strings.Contains(prompt, "call Mara") || !strings.Contains(prompt, "I called Mara today") {
		t.Fatalf("prompt misses the commitment or transcript: %.200q", prompt)
	}
	if _, _, err := client.GenerateResolution(t.Context(), "flash", "", "words"); !errors.Is(err, gemini.ErrInvalid) {
		t.Fatalf("empty commitment error = %v, want ErrInvalid", err)
	}
}
