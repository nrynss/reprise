package analysis

// Mention is one stored callback anchor with its rendered word offset.
// The memory pass reads these rows to open later episodes on earlier
// ones, and the keyterm boost reads their quotes.
type Mention struct {
	// Kind names the mention kind. Entities keep the provider type, such
	// as person_name. Key phrase occurrences read keyphrase.
	Kind string
	// Offset is the rendered word index the mention points at.
	Offset int
	// Quote is the mention wording as heard.
	Quote string
}

// KeyphraseKind marks mentions built from key phrase occurrences.
const KeyphraseKind = "keyphrase"

// OffsetOf maps one millisecond span to its rendered word index. It
// returns the first word overlapping the span, or the next word after
// a span sitting in a gap, or the last word past the end. It returns
// minus one when no word can hold the span.
func OffsetOf(words []Word, startMs, endMs int64) int {
	if len(words) == 0 || endMs < startMs {
		return -1
	}
	for i, w := range words {
		if w.StartMs < endMs && w.EndMs > startMs {
			return i
		}
	}
	for i, w := range words {
		if w.EndMs > startMs {
			return i
		}
	}
	return len(words) - 1
}

// MentionsFromEntities maps every entity to its rendered word offset.
// Spans outside the timeline attach to the nearest edge word, so drift
// never loses a real entity.
func MentionsFromEntities(entities []Entity, words []Word) []Mention {
	out := make([]Mention, 0, len(entities))
	for _, e := range entities {
		offset := OffsetOf(words, e.StartMs, e.EndMs)
		if offset < 0 {
			continue
		}
		out = append(out, Mention{Kind: e.Type, Offset: offset, Quote: e.Text})
	}
	return out
}

// MentionsFromPhrases maps every key phrase occurrence to its rendered
// word offset. One phrase with three spans becomes three mentions, so
// each occurrence anchors its own callback.
func MentionsFromPhrases(phrases []KeyPhrase, words []Word) []Mention {
	out := make([]Mention, 0, len(phrases))
	for _, p := range phrases {
		for _, s := range p.Spans {
			offset := OffsetOf(words, s.StartMs, s.EndMs)
			if offset < 0 {
				continue
			}
			out = append(out, Mention{Kind: KeyphraseKind, Offset: offset, Quote: p.Text})
		}
	}
	return out
}
