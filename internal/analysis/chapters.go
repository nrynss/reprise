package analysis

import (
	"fmt"
	"strings"
)

// continuedSuffix marks a chapter split from a longer span, so exports
// read honestly where the model named one chapter and the pass cut two.
const continuedSuffix = " (continued)"

// SegmentWords groups words into timed slices for the chapter prompt. A
// slice ends on closing punctuation, on a long pause, or at the word
// cap, so each slice reads as one thought with its start time.
func SegmentWords(words []Word) []Segment {
	const gapMs = 1500
	const capWords = 25
	var out []Segment
	var cur []Word
	flush := func() {
		if len(cur) == 0 {
			return
		}
		parts := make([]string, 0, len(cur))
		for _, w := range cur {
			parts = append(parts, w.Text)
		}
		out = append(out, Segment{StartMs: cur[0].StartMs, Text: strings.Join(parts, " ")})
		cur = nil
	}
	for i, w := range words {
		if len(cur) > 0 {
			prev := cur[len(cur)-1]
			if w.StartMs-prev.EndMs >= gapMs || len(cur) >= capWords || endsThought(prev.Text) {
				flush()
			}
		}
		cur = append(cur, w)
		if i == len(words)-1 {
			flush()
		}
	}
	return out
}

// endsThought reports whether a word closes a thought for segmenting.
func endsThought(text string) bool {
	return strings.HasSuffix(text, ".") || strings.HasSuffix(text, "!") || strings.HasSuffix(text, "?")
}

// PromptText builds the chapter prompt from timed segments. It states
// the three chapter rules outright, so the answer arrives shaped and
// the pass only enforces what the model missed.
func PromptText(segments []Segment, durationSecs float64) string {
	var body strings.Builder
	for _, s := range segments {
		fmt.Fprintf(&body, "[%s] %s\n", formatClock(s.StartMs), s.Text)
	}
	return "Split this episode transcript into chapters. Return a JSON array " +
		"where each item holds a title string and a start_ms number. " +
		"Rules. The first chapter starts at 0. Every chapter lasts at least " +
		"10 seconds. Name at least 3 chapters when the episode allows. " +
		fmt.Sprintf("The episode runs %.0f seconds.\n", durationSecs) + body.String()
}

// formatClock renders milliseconds as minutes and seconds for the prompt.
func formatClock(ms int64) string {
	if ms < 0 {
		ms = 0
	}
	return fmt.Sprintf("%02d:%02d", ms/60000, (ms%60000)/1000)
}

// NormalizeChapters shapes raw drafts into episode chapters. It sorts by
// start, pins the first chapter at zero, snaps every other start to the
// word it lands on, merges spans under the minimum length, and splits
// the longest spans until a long enough episode carries three chapters.
// Seeking to any returned start lands on the words the chapter names,
// because each start equals a stored word start or zero.
func NormalizeChapters(drafts []ChapterDraft, words []Word, durationMs int64) ([]Chapter, error) {
	if len(words) == 0 || len(drafts) == 0 {
		return nil, fmt.Errorf("analysis: shape %d drafts over %d words: %w", len(drafts), len(words), ErrChapters)
	}
	if durationMs <= 0 {
		durationMs = words[len(words)-1].EndMs
	}
	sorted := append([]ChapterDraft(nil), drafts...)
	sortDrafts(sorted)
	cleaned := make([]ChapterDraft, 0, len(sorted))
	for _, d := range sorted {
		if strings.TrimSpace(d.Title) == "" {
			return nil, fmt.Errorf("analysis: shape draft at %d: %w: empty title", d.StartMs, ErrChapters)
		}
		if d.StartMs < 0 {
			d.StartMs = 0
		}
		if durationMs > 0 && d.StartMs >= durationMs {
			continue
		}
		if len(cleaned) > 0 && cleaned[len(cleaned)-1].StartMs == d.StartMs {
			continue
		}
		cleaned = append(cleaned, d)
	}
	if len(cleaned) == 0 {
		return nil, fmt.Errorf("analysis: shape drafts past %d ms: %w", durationMs, ErrChapters)
	}
	cleaned[0].StartMs = 0
	snapped := make([]Chapter, 0, len(cleaned))
	for i, d := range cleaned {
		start := d.StartMs
		if i > 0 {
			start = snapStart(words, d.StartMs)
		}
		if len(snapped) > 0 && snapped[len(snapped)-1].StartMs == start {
			continue
		}
		snapped = append(snapped, Chapter{Title: d.Title, StartMs: start})
	}
	merged := mergeShort(snapped, durationMs)
	return ensureCount(merged, words, durationMs), nil
}

// sortDrafts orders drafts by start with a stable insertion pass. Equal
// starts keep provider order, so the first title wins the dedupe.
func sortDrafts(drafts []ChapterDraft) {
	for i := 1; i < len(drafts); i++ {
		for j := i; j > 0 && drafts[j].StartMs < drafts[j-1].StartMs; j-- {
			drafts[j], drafts[j-1] = drafts[j-1], drafts[j]
		}
	}
}

// snapStart moves one chapter start to the word holding it. It returns
// the first word ending after the stamp, or the last word start past
// the end, so the seek lands on speech either way.
func snapStart(words []Word, startMs int64) int64 {
	for _, w := range words {
		if w.EndMs > startMs {
			return w.StartMs
		}
	}
	return words[len(words)-1].StartMs
}

// mergeShort folds spans under the minimum into their neighbour. It
// removes the chapter owning each short span, so titles stay with their
// opening words and only the cut between spans moves.
func mergeShort(chapters []Chapter, durationMs int64) []Chapter {
	out := append([]Chapter(nil), chapters...)
	for {
		dd := dropIndex(out, durationMs)
		if dd < 0 {
			return out
		}
		out = append(out[:dd], out[dd+1:]...)
	}
}

// dropIndex finds the chapter owning the first short span. A short
// opening span drops the second chapter into the first, so the episode
// still opens at zero. Any other short span drops its owner into the
// previous chapter. A short closing span drops the last chapter. It
// returns minus one when every span lasts long enough.
func dropIndex(chapters []Chapter, durationMs int64) int {
	if len(chapters) < 2 {
		return -1
	}
	for i := 0; i+1 < len(chapters); i++ {
		if chapters[i+1].StartMs-chapters[i].StartMs < MinChapterMs {
			if i == 0 {
				return 1
			}
			return i
		}
	}
	if durationMs > 0 && durationMs-chapters[len(chapters)-1].StartMs < MinChapterMs {
		return len(chapters) - 1
	}
	return -1
}

// ensureCount splits the longest spans until a long enough episode
// carries three chapters. Episodes that cannot fit three ten second spans
// keep what merging left, because padding would invent structure.
func ensureCount(chapters []Chapter, words []Word, durationMs int64) []Chapter {
	target := WantChapters
	if feasible := int(durationMs / MinChapterMs); feasible < target {
		target = feasible
	}
	if target < 1 {
		target = 1
	}
	out := append([]Chapter(nil), chapters...)
	for len(out) < target {
		at, boundary := splitLongest(out, words, durationMs)
		if at < 0 {
			return out
		}
		extra := Chapter{Title: out[at].Title + continuedSuffix, StartMs: boundary}
		out = append(out[:at+1], append([]Chapter{extra}, out[at+1:]...)...)
	}
	return out
}

// splitLongest finds the longest chapter with a splittable word boundary
// strictly inside it and returns its index plus that boundary. A boundary
// splits only when both halves stay at least the minimum chapter long. It
// returns minus one when no chapter can split, so short episodes keep
// fewer chapters instead of cutting a sliver.
func splitLongest(chapters []Chapter, words []Word, durationMs int64) (int, int64) {
	at := -1
	var span int64
	for i := range chapters {
		end := durationMs
		if i+1 < len(chapters) {
			end = chapters[i+1].StartMs
		}
		if length := end - chapters[i].StartMs; length > span {
			if _, ok := middleBoundary(words, chapters[i].StartMs, end); ok {
				at = i
				span = length
			}
		}
	}
	if at < 0 {
		return -1, 0
	}
	end := durationMs
	if at+1 < len(chapters) {
		end = chapters[at+1].StartMs
	}
	boundary, _ := middleBoundary(words, chapters[at].StartMs, end)
	return at, boundary
}

// middleBoundary returns the word start nearest the middle of one span
// that leaves both halves at least the minimum chapter long. It reports
// false when no word start inside the span satisfies the floor on both
// sides, so the caller keeps fewer chapters instead of cutting a sliver.
func middleBoundary(words []Word, startMs, endMs int64) (int64, bool) {
	mid := startMs + (endMs-startMs)/2
	best := int64(-1)
	var distance int64 = 1 << 62
	for _, w := range words {
		if w.StartMs <= startMs || w.StartMs >= endMs {
			continue
		}
		if w.StartMs-startMs < MinChapterMs || endMs-w.StartMs < MinChapterMs {
			continue
		}
		gap := w.StartMs - mid
		if gap < 0 {
			gap = -gap
		}
		if gap < distance {
			distance = gap
			best = w.StartMs
		}
	}
	return best, best >= 0
}
