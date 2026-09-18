package editorial

import (
	"fmt"
	"log/slog"
	"strings"
)

// ColdOpenMinMs floors the opening span at ten seconds. A shorter span
// cannot open an episode.
const ColdOpenMinMs = 10_000

// ColdOpenMaxMs caps the opening span at twenty seconds. A longer span is
// a chapter, not a cold open.
const ColdOpenMaxMs = 20_000

// word is one timeline word the proposals point at. Offsets are positions
// in this slice, which the prompt numbers the same way.
type word struct {
	// Text is the word as heard, with its original casing.
	Text string
	// StartMs is the word start in milliseconds on the episode clock.
	StartMs int64
	// EndMs is the word end in milliseconds on the episode clock.
	EndMs int64
}

// draft holds the validated proposals one run stores. Ranges are timeline
// offsets. The title and show notes are trimmed text.
type draft struct {
	// ColdOpen carries the opening span. Nil means the model sent none or
	// the span failed validation.
	ColdOpen *[2]int
	// ColdReason carries the delivery note behind the opening span.
	ColdReason string
	// Cuts carries every surviving removal with its reason.
	Cuts []storedCut
	// Title names the episode. Empty means the plain title applies.
	Title string
	// ShowNotes carries a few sentences in the speaker's own words.
	ShowNotes string
	// Callback carries the unresolved thing with its quote and span.
	Callback *storedCallback
}

// storedCut is one removal that survived validation.
type storedCut struct {
	// Start is the offset of the first word to remove.
	Start int
	// End is the offset of the last word to remove.
	End int
	// Reason says in one line why this span goes.
	Reason string
}

// storedCallback is the unresolved thing that survived validation.
type storedCallback struct {
	// Start is the offset of the first quoted word.
	Start int
	// Quote carries the speaker's own words, quoted exactly.
	Quote string
	// Text names the unresolved thing in one line.
	Text string
}

// inRange reports whether start through end names real words. A proposal
// pointing outside the timeline is dangling.
func inRange(words []word, start, end int) bool {
	return start >= 0 && end >= start && end < len(words)
}

// validate checks every proposal against the loaded timeline. Dangling
// spans are dropped and logged, never stored. An empty timeline drops
// every span and keeps the title and notes, so the draft still renders.
func validate(log *slog.Logger, words []word, got answer) draft {
	out := draft{Title: strings.TrimSpace(got.Title), ShowNotes: strings.TrimSpace(got.ShowNotes)}
	if got.ColdOpen != nil {
		start, end := got.ColdOpen.StartWord, got.ColdOpen.EndWord
		switch {
		case !inRange(words, start, end):
			log.Warn("editorial: cold open points outside the timeline", "start", start, "end", end, "words", len(words))
		case words[end].EndMs-words[start].StartMs < ColdOpenMinMs || words[end].EndMs-words[start].StartMs > ColdOpenMaxMs:
			log.Warn("editorial: cold open misses the 10 to 20 second span",
				"start", start, "end", end, "span_ms", words[end].EndMs-words[start].StartMs)
		default:
			out.ColdOpen = &[2]int{start, end}
			out.ColdReason = strings.TrimSpace(got.ColdOpen.Reason)
		}
	}
	for _, proposed := range got.Cuts {
		reason := strings.TrimSpace(proposed.Reason)
		switch {
		case !inRange(words, proposed.StartWord, proposed.EndWord):
			log.Warn("editorial: cut points outside the timeline",
				"start", proposed.StartWord, "end", proposed.EndWord, "words", len(words))
		case reason == "":
			log.Warn("editorial: cut carries no reason", "start", proposed.StartWord, "end", proposed.EndWord)
		default:
			out.Cuts = append(out.Cuts, storedCut{Start: proposed.StartWord, End: proposed.EndWord, Reason: reason})
		}
	}
	if got.Callback != nil {
		quote := strings.TrimSpace(got.Callback.Quote)
		text := strings.TrimSpace(got.Callback.Text)
		switch {
		case !inRange(words, got.Callback.StartWord, got.Callback.EndWord):
			log.Warn("editorial: callback points outside the timeline",
				"start", got.Callback.StartWord, "end", got.Callback.EndWord, "words", len(words))
		case quote == "" || text == "":
			log.Warn("editorial: callback misses its quote or text")
		default:
			out.Callback = &storedCallback{Start: got.Callback.StartWord, Quote: quote, Text: text}
		}
	}
	return out
}

// timeline numbers every word by its offset with its timing, in the order
// the proposals point at. The prompt carries this text beside the stems,
// so the model reads the words and hears the delivery together.
//
// The indexing is positional and starts from zero.
func timeline(words []word) string {
	var out strings.Builder
	for i, w := range words {
		fmt.Fprintf(&out, "[%d] %s (%d-%d)\n", i, w.Text, w.StartMs, w.EndMs)
	}
	return out.String()
}
