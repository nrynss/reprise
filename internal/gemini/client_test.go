package gemini_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"google.golang.org/genai"

	"github.com/nrynss/reprise/internal/gemini"
)

// answer builds a canned model response with one candidate and usage.
func answer(text string) *genai.GenerateContentResponse {
	return &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{{
			Content:      &genai.Content{Role: "model", Parts: []*genai.Part{{Text: text}}},
			FinishReason: genai.FinishReasonStop,
		}},
		UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
			PromptTokenCount: 12, CandidatesTokenCount: 5, TotalTokenCount: 17,
		},
	}
}

// request carries one editorial pass with two short stems.
func request() gemini.EditorialRequest {
	return gemini.EditorialRequest{
		UserStem: []byte{0x4f, 0x70, 0x75, 0x73},
		HostStem: []byte{0x53, 0x74, 0x65, 0x6d},
		Timeline: "[0] hello [1] world",
	}
}

// TestGenerateReturnsTextAndUsage checks the client returns the answer text
// with its token counts through the scripted backend.
func TestGenerateReturnsTextAndUsage(t *testing.T) {
	t.Parallel()
	client := gemini.NewTestClient(func(context.Context, string, []*genai.Content, *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
		return answer(`{"title":"Harbor Light"}`), nil
	})
	text, usage, err := client.Generate(t.Context(), "flash", []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "hi"}}}}, nil)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if !strings.Contains(text, "Harbor Light") {
		t.Fatalf("text = %q, want the scripted answer", text)
	}
	if usage.Prompt != 12 || usage.Candidates != 5 || usage.Total != 17 {
		t.Fatalf("usage = %+v, want 12, 5, and 17", usage)
	}
}

// TestGenerateFailsEmpty checks an answer with no text fails with the
// empty sentinel, so the caller falls back to a renderable draft.
func TestGenerateFailsEmpty(t *testing.T) {
	t.Parallel()
	client := gemini.NewTestClient(func(context.Context, string, []*genai.Content, *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
		return &genai.GenerateContentResponse{}, nil
	})
	_, _, err := client.Generate(t.Context(), "flash", []*genai.Content{{Role: "user"}}, nil)
	if !errors.Is(err, gemini.ErrEmpty) {
		t.Fatalf("error = %v, want ErrEmpty", err)
	}
}

// TestGenerateWrapsBackendFailure checks a backend error fails with the
// generate sentinel, so the caller settles nothing and falls back.
func TestGenerateWrapsBackendFailure(t *testing.T) {
	t.Parallel()
	boom := errors.New("socket closed")
	client := gemini.NewTestClient(func(context.Context, string, []*genai.Content, *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
		return nil, boom
	})
	_, _, err := client.Generate(t.Context(), "flash", []*genai.Content{{Role: "user"}}, nil)
	if !errors.Is(err, gemini.ErrGenerate) || !errors.Is(err, boom) {
		t.Fatalf("error = %v, want ErrGenerate wrapping the backend failure", err)
	}
}

// TestGenerateEditorialSendsBothStems checks one editorial call carries the
// timeline plus both stems as audio parts, with the JSON schema attached.
func TestGenerateEditorialSendsBothStems(t *testing.T) {
	t.Parallel()
	var gotContents []*genai.Content
	var gotConfig *genai.GenerateContentConfig
	var gotModel string
	client := gemini.NewTestClient(func(_ context.Context, model string, contents []*genai.Content, config *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
		gotModel, gotContents, gotConfig = model, contents, config
		return answer(`{"title":"Harbor Light"}`), nil
	})
	reply, err := client.GenerateEditorial(t.Context(), "flash", request())
	if err != nil {
		t.Fatalf("editorial: %v", err)
	}
	if !strings.Contains(reply.JSON, "Harbor Light") {
		t.Fatalf("answer = %q, want the scripted JSON", reply.JSON)
	}
	if gotModel != "flash" {
		t.Fatalf("model = %q, want the settings model passed through", gotModel)
	}
	if len(gotContents) != 1 || len(gotContents[0].Parts) != 3 {
		t.Fatalf("parts = %d, want the prompt plus two stems", len(gotContents[0].Parts))
	}
	prompt := gotContents[0].Parts[0].Text
	if !strings.Contains(prompt, "Do not pick from the transcript alone") {
		t.Fatalf("prompt misses the delivery rule: %.200q", prompt)
	}
	if !strings.Contains(prompt, "[0] hello [1] world") {
		t.Fatalf("prompt misses the timeline: %.200q", prompt)
	}
	for _, part := range gotContents[0].Parts[1:] {
		data := part.InlineData
		if data == nil || data.MIMEType != "audio/ogg" || len(data.Data) == 0 {
			t.Fatalf("stem part = %+v, want Opus audio bytes", part)
		}
	}
	if gotConfig == nil || gotConfig.ResponseMIMEType != "application/json" || gotConfig.ResponseSchema == nil {
		t.Fatalf("config = %+v, want the JSON schema attached", gotConfig)
	}
	if _, ok := gotConfig.ResponseSchema.Properties["cold_open"]; !ok {
		t.Fatal("schema misses the cold open")
	}
}

// TestGenerateEditorialRefusesEmptyStems checks empty audio never reaches
// the backend, so a programming error cannot spend on silence.
func TestGenerateEditorialRefusesEmptyStems(t *testing.T) {
	t.Parallel()
	calls := 0
	client := gemini.NewTestClient(func(context.Context, string, []*genai.Content, *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
		calls++
		return answer(`{}`), nil
	})
	bad := request()
	bad.UserStem = nil
	if _, err := client.GenerateEditorial(t.Context(), "flash", bad); !errors.Is(err, gemini.ErrInvalid) {
		t.Fatalf("error = %v, want ErrInvalid", err)
	}
	if calls != 0 {
		t.Fatalf("backend calls = %d, want none for empty audio", calls)
	}
}

// TestNewClientRefusesEmptyConfig checks an empty project, location, or
// credential fails before any credential parsing runs.
func TestNewClientRefusesEmptyConfig(t *testing.T) {
	t.Parallel()
	if _, err := gemini.NewClient(t.Context(), gemini.Config{}); !errors.Is(err, gemini.ErrInvalid) {
		t.Fatalf("error = %v, want ErrInvalid", err)
	}
}

// capped builds a canned model response cut off by the output cap. The
// text is a JSON fragment, the way a capped editorial answer arrives.
func capped(text string) *genai.GenerateContentResponse {
	return &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{{
			Content:      &genai.Content{Role: "model", Parts: []*genai.Part{{Text: text}}},
			FinishReason: genai.FinishReasonMaxTokens,
		}},
		UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
			PromptTokenCount: 12, CandidatesTokenCount: 5, TotalTokenCount: 17,
		},
	}
}

// TestGenerateNamesTruncation checks a reply stopped by the output cap
// fails with the truncation sentinel, so the caller names the cap instead
// of decoding the fragment. The same fragment with a stop reason still
// succeeds, which proves the sentinel keys on the finish reason.
func TestGenerateNamesTruncation(t *testing.T) {
	t.Parallel()
	fragment := `{"title":"Harbor`
	client := gemini.NewTestClient(func(context.Context, string, []*genai.Content, *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
		return capped(fragment), nil
	})
	_, _, err := client.Generate(t.Context(), "flash", []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "hi"}}}}, nil)
	if !errors.Is(err, gemini.ErrTruncated) {
		t.Fatalf("error = %v, want ErrTruncated", err)
	}
	if errors.Is(err, gemini.ErrEmpty) || errors.Is(err, gemini.ErrGenerate) {
		t.Fatalf("error = %v, want only ErrTruncated", err)
	}
	stopped := gemini.NewTestClient(func(context.Context, string, []*genai.Content, *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
		return answer(fragment), nil
	})
	text, _, err := stopped.Generate(t.Context(), "flash", []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "hi"}}}}, nil)
	if err != nil {
		t.Fatalf("stopped fragment: %v", err)
	}
	if text != fragment {
		t.Fatalf("text = %q, want the fragment passed through", text)
	}
}

// TestGenerateEditorialBoundsReasoning checks the editorial call bounds
// its reasoning budget inside the output cap, so reasoning cannot starve
// the answer on a long take.
func TestGenerateEditorialBoundsReasoning(t *testing.T) {
	t.Parallel()
	var gotConfig *genai.GenerateContentConfig
	client := gemini.NewTestClient(func(_ context.Context, _ string, _ []*genai.Content, config *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
		gotConfig = config
		return answer(`{"title":"Harbor Light"}`), nil
	})
	if _, err := client.GenerateEditorial(t.Context(), "flash", request()); err != nil {
		t.Fatalf("editorial: %v", err)
	}
	if gotConfig == nil || gotConfig.ThinkingConfig == nil || gotConfig.ThinkingConfig.ThinkingBudget == nil {
		t.Fatalf("config = %+v, want an explicit reasoning budget", gotConfig)
	}
	budget := *gotConfig.ThinkingConfig.ThinkingBudget
	if budget <= 0 || gotConfig.MaxOutputTokens <= budget {
		t.Fatalf("budget %d cap %d, want a positive budget inside the cap", budget, gotConfig.MaxOutputTokens)
	}
}

// TestGenerateEditorialReportsTruncation checks a capped editorial answer
// reaches the caller as the truncation sentinel, never as a fragment the
// run would misdecode.
func TestGenerateEditorialReportsTruncation(t *testing.T) {
	t.Parallel()
	client := gemini.NewTestClient(func(context.Context, string, []*genai.Content, *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
		return capped(`{"cold_open":{"start_word":0`), nil
	})
	if _, err := client.GenerateEditorial(t.Context(), "flash", request()); !errors.Is(err, gemini.ErrTruncated) {
		t.Fatalf("error = %v, want ErrTruncated", err)
	}
}
