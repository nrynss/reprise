package analysis_test

import (
	"testing"

	"github.com/nrynss/reprise/internal/analysis"
)

// scheduleWords builds count words spaced stepMs apart for offset tests.
func scheduleWords(count int, stepMs int64) []analysis.Word {
	words := make([]analysis.Word, 0, count)
	for i := range count {
		start := int64(i) * stepMs
		words = append(words, analysis.Word{Text: "w", StartMs: start, EndMs: start + stepMs - 200})
	}
	return words
}

// TestOffsetOfMapsSpans pins exact word indexes for overlapping spans,
// gaps, and spans past the end.
func TestOffsetOfMapsSpans(t *testing.T) {
	t.Parallel()
	words := scheduleWords(6, 1000)
	cases := []struct {
		name       string
		start, end int64
		want       int
	}{
		{"overlap lands on its word", 2100, 2400, 2},
		{"span over two words lands on the first", 700, 2100, 0},
		{"gap takes the next word", 850, 900, 1},
		{"span before the first word takes it", 0, 100, 0},
		{"span past the end takes the last word", 9000, 9500, 5},
		{"empty span takes its word", 2000, 2000, 2},
	}
	for _, c := range cases {
		if got := analysis.OffsetOf(words, c.start, c.end); got != c.want {
			t.Fatalf("%s: offset = %d, want %d", c.name, got, c.want)
		}
	}
}

// TestOffsetOfRefusesEmpty pins minus one for an empty timeline and an
// inverted span.
func TestOffsetOfRefusesEmpty(t *testing.T) {
	t.Parallel()
	if got := analysis.OffsetOf(nil, 0, 100); got != -1 {
		t.Fatalf("empty offset = %d, want -1", got)
	}
	words := scheduleWords(3, 1000)
	if got := analysis.OffsetOf(words, 500, 100); got != -1 {
		t.Fatalf("inverted offset = %d, want -1", got)
	}
}

// TestMentionsFromEntities pins kinds, offsets, and quotes for entity
// mentions, with drift past the end attaching to the last word.
func TestMentionsFromEntities(t *testing.T) {
	t.Parallel()
	words := scheduleWords(10, 1000)
	entities := []analysis.Entity{
		{Type: "person_name", Text: "Mara", StartMs: 5100, EndMs: 5400},
		{Type: "location", Text: "Lisbon", StartMs: 20000, EndMs: 20500},
	}
	got := analysis.MentionsFromEntities(entities, words)
	if len(got) != 2 {
		t.Fatalf("mentions = %+v, want both entities kept", got)
	}
	if got[0].Kind != "person_name" || got[0].Offset != 5 || got[0].Quote != "Mara" {
		t.Fatalf("mention = %+v, want the person kind at word 5", got[0])
	}
	if got[1].Offset != 9 {
		t.Fatalf("drifted mention offset = %d, want the last word", got[1].Offset)
	}
	if len(analysis.MentionsFromEntities(entities, nil)) != 0 {
		t.Fatal("entities over no words built mentions")
	}
}

// TestMentionsFromPhrases pins one mention per occurrence span with the
// keyphrase kind.
func TestMentionsFromPhrases(t *testing.T) {
	t.Parallel()
	words := scheduleWords(10, 1000)
	phrases := []analysis.KeyPhrase{{
		Text: "paper crane", Rank: 0.4, Count: 2,
		Spans: []analysis.Span{{StartMs: 2100, EndMs: 2600}, {StartMs: 7100, EndMs: 7600}},
	}}
	got := analysis.MentionsFromPhrases(phrases, words)
	if len(got) != 2 {
		t.Fatalf("mentions = %+v, want one per occurrence", got)
	}
	if got[0].Kind != analysis.KeyphraseKind || got[0].Offset != 2 || got[0].Quote != "paper crane" {
		t.Fatalf("first = %+v, want the keyphrase kind at word 2", got[0])
	}
	if got[1].Offset != 7 {
		t.Fatalf("second offset = %d, want 7", got[1].Offset)
	}
}
