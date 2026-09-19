package analysis_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/nrynss/reprise/internal/analysis"
)

// longWords builds 40 words across 44 seconds for chapter tests.
func longWords() []analysis.Word {
	words := make([]analysis.Word, 0, 40)
	for i := range 40 {
		start := int64(500 + i*1100)
		text := "word"
		if i%10 == 9 {
			text = "end."
		}
		words = append(words, analysis.Word{Text: text, StartMs: start, EndMs: start + 700})
	}
	return words
}

// startsOnWord reports whether ms equals zero or a stored word start.
func startsOnWord(words []analysis.Word, ms int64) bool {
	if ms == 0 {
		return true
	}
	for _, w := range words {
		if w.StartMs == ms {
			return true
		}
	}
	return false
}

// TestNormalizeShapesDrafts pins sorting, the zero first start, and
// snapping every other start to its word.
func TestNormalizeShapesDrafts(t *testing.T) {
	t.Parallel()
	words := longWords()
	drafts := []analysis.ChapterDraft{
		{Title: "Third", StartMs: 23000},
		{Title: "First", StartMs: 400},
		{Title: "Second", StartMs: 11500},
		{Title: "Fourth", StartMs: 33500},
	}
	got, err := analysis.NormalizeChapters(drafts, words, 44500)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("chapters = %+v, want all four", got)
	}
	if got[0].Title != "First" || got[0].StartMs != 0 {
		t.Fatalf("first = %+v, want First at zero", got[0])
	}
	for _, c := range got[1:] {
		if !startsOnWord(words, c.StartMs) {
			t.Fatalf("chapter %+v starts off the word grid", c)
		}
	}
}

// TestNormalizeMergesShortSpans drops the later boundary of every span
// under ten seconds.
func TestNormalizeMergesShortSpans(t *testing.T) {
	t.Parallel()
	words := longWords()
	drafts := []analysis.ChapterDraft{
		{Title: "First", StartMs: 0},
		{Title: "Sliver", StartMs: 11500},
		{Title: "Second", StartMs: 14000},
		{Title: "Third", StartMs: 26000},
	}
	got, err := analysis.NormalizeChapters(drafts, words, 44500)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	for _, c := range got {
		if c.Title == "Sliver" {
			t.Fatalf("chapters = %+v, want the short span merged away", got)
		}
	}
	if len(got) != 3 || got[0].Title != "First" || got[1].Title != "Second" {
		t.Fatalf("chapters = %+v, want First, Second, Third", got)
	}
}

// TestNormalizeDropsTrailingSliver merges a short last span into its
// neighbour, then restores the count by splitting, because the episode
// still allows three chapters.
func TestNormalizeDropsTrailingSliver(t *testing.T) {
	t.Parallel()
	words := longWords()
	drafts := []analysis.ChapterDraft{
		{Title: "First", StartMs: 0},
		{Title: "Second", StartMs: 12000},
		{Title: "Stub", StartMs: 40000},
	}
	got, err := analysis.NormalizeChapters(drafts, words, 44500)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("chapters = %+v, want three after the split", got)
	}
	for _, c := range got {
		if c.Title == "Stub" {
			t.Fatalf("chapters = %+v, want the trailing stub merged away", got)
		}
	}
	if got[1].Title != "Second" {
		t.Fatalf("chapters = %+v, want Second kept", got)
	}
}

// TestNormalizeSplitsToThree pins the minimum count on a long episode:
// one draft becomes three chapters with continued titles, each on the
// word grid and at least ten seconds long.
func TestNormalizeSplitsToThree(t *testing.T) {
	t.Parallel()
	words := longWords()
	drafts := []analysis.ChapterDraft{{Title: "Whole", StartMs: 0}}
	got, err := analysis.NormalizeChapters(drafts, words, 44500)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("chapters = %+v, want three", got)
	}
	if got[0].Title != "Whole" || got[0].StartMs != 0 {
		t.Fatalf("first = %+v, want Whole at zero", got[0])
	}
	for i, c := range got {
		if !startsOnWord(words, c.StartMs) {
			t.Fatalf("chapter %+v starts off the word grid", c)
		}
		end := int64(44500)
		if i+1 < len(got) {
			end = got[i+1].StartMs
		}
		if end-c.StartMs < analysis.MinChapterMs {
			t.Fatalf("chapter %+v runs %d ms, want at least ten seconds", c, end-c.StartMs)
		}
		if i > 0 && !strings.HasSuffix(c.Title, " (continued)") {
			t.Fatalf("chapter %+v misses the continued mark", c)
		}
	}
}

// TestNormalizeKeepsShortEpisodeWhole checks a two second episode
// carries one chapter instead of padded structure.
func TestNormalizeKeepsShortEpisodeWhole(t *testing.T) {
	t.Parallel()
	words := scheduleWords(8, 250)
	drafts := []analysis.ChapterDraft{{Title: "Whole", StartMs: 0}}
	got, err := analysis.NormalizeChapters(drafts, words, 2000)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if len(got) != 1 || got[0].StartMs != 0 {
		t.Fatalf("chapters = %+v, want one chapter at zero", got)
	}
}

// TestNormalizeSplitFloorKeepsTenSeconds pins the split floor on a 32
// second episode. Middle cuts would leave eight second chapters, so the
// pass keeps two valid chapters instead of three short ones.
func TestNormalizeSplitFloorKeepsTenSeconds(t *testing.T) {
	t.Parallel()
	words := scheduleWords(40, 800)
	drafts := []analysis.ChapterDraft{{Title: "Whole", StartMs: 0}}
	got, err := analysis.NormalizeChapters(drafts, words, 32000)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("chapters = %+v, want two floor keeping chapters", got)
	}
	for i, c := range got {
		if !startsOnWord(words, c.StartMs) {
			t.Fatalf("chapter %+v starts off the word grid", c)
		}
		end := int64(32000)
		if i+1 < len(got) {
			end = got[i+1].StartMs
		}
		if end-c.StartMs < analysis.MinChapterMs {
			t.Fatalf("chapter %+v runs %d ms, want at least ten seconds", c, end-c.StartMs)
		}
		if i > 0 && !strings.HasSuffix(c.Title, " (continued)") {
			t.Fatalf("chapter %+v misses the continued mark", c)
		}
	}
}

// TestNormalizeShortEpisodeKeepsFewer pins a 25 second episode at two
// chapters. The duration allows two ten second spans but not three, so
// the pass stops splitting instead of cutting a sliver.
func TestNormalizeShortEpisodeKeepsFewer(t *testing.T) {
	t.Parallel()
	words := scheduleWords(40, 800)
	drafts := []analysis.ChapterDraft{{Title: "Whole", StartMs: 0}}
	got, err := analysis.NormalizeChapters(drafts, words, 25000)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("chapters = %+v, want two", got)
	}
	for i, c := range got {
		end := int64(25000)
		if i+1 < len(got) {
			end = got[i+1].StartMs
		}
		if end-c.StartMs < analysis.MinChapterMs {
			t.Fatalf("chapter %+v runs %d ms, want at least ten seconds", c, end-c.StartMs)
		}
	}
}

// TestNormalizeRejectsUnusable pins the sentinel for empty drafts,
// blank titles, and drafts past the episode end.
func TestNormalizeRejectsUnusable(t *testing.T) {
	t.Parallel()
	words := longWords()
	for _, drafts := range [][]analysis.ChapterDraft{
		nil,
		{{Title: "", StartMs: 0}},
		{{Title: "Late", StartMs: 90000}},
	} {
		if _, err := analysis.NormalizeChapters(drafts, words, 44500); !errors.Is(err, analysis.ErrChapters) {
			t.Fatalf("drafts %+v error = %v, want the chapters sentinel", drafts, err)
		}
	}
	if _, err := analysis.NormalizeChapters([]analysis.ChapterDraft{{Title: "One", StartMs: 0}}, nil, 44500); !errors.Is(err, analysis.ErrChapters) {
		t.Fatalf("empty words error = %v, want the chapters sentinel", err)
	}
}

// TestNormalizeDedupesEqualStarts keeps the first title on a shared
// start, then restores the count by splitting.
func TestNormalizeDedupesEqualStarts(t *testing.T) {
	t.Parallel()
	words := longWords()
	drafts := []analysis.ChapterDraft{
		{Title: "Keep", StartMs: 12000},
		{Title: "Drop", StartMs: 12000},
		{Title: "First", StartMs: 0},
	}
	got, err := analysis.NormalizeChapters(drafts, words, 44500)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if len(got) != 3 || got[1].Title != "Keep" {
		t.Fatalf("chapters = %+v, want First, Keep, and a split", got)
	}
	for _, c := range got {
		if c.Title == "Drop" {
			t.Fatalf("chapters = %+v, want Drop deduped away", got)
		}
	}
}

// TestSegmentWordsBreaksOnThoughts checks segments split on closing
// punctuation with their start times.
func TestSegmentWordsBreaksOnThoughts(t *testing.T) {
	t.Parallel()
	words := []analysis.Word{
		{Text: "Mara", StartMs: 100, EndMs: 300},
		{Text: "came.", StartMs: 350, EndMs: 600},
		{Text: "Quilby", StartMs: 800, EndMs: 1100},
		{Text: "mapped.", StartMs: 1150, EndMs: 1400},
	}
	segments := analysis.SegmentWords(words)
	if len(segments) != 2 {
		t.Fatalf("segments = %+v, want two thoughts", segments)
	}
	if segments[0].StartMs != 100 || segments[0].Text != "Mara came." {
		t.Fatalf("first = %+v, want Mara came at 100", segments[0])
	}
}

// TestPromptTextStatesTheRules pins the three chapter rules and the
// transcript words in the prompt the model reads.
func TestPromptTextStatesTheRules(t *testing.T) {
	t.Parallel()
	segments := []analysis.Segment{{StartMs: 0, Text: "Mara came."}}
	prompt := analysis.PromptText(segments, 45)
	for _, want := range []string{"start_ms", "10 seconds", "3 chapters", "Mara came.", "[00:00]"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt misses %q:\n%s", want, prompt)
		}
	}
}
