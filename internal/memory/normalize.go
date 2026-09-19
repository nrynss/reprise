package memory

import (
	"strings"
	"unicode"
)

// NormalizeName folds a mention quote into its grouping key. It lowercases
// the text, trims surrounding space and punctuation, and collapses inner
// whitespace to single spaces. Inner punctuation stays, so possessives
// keep their shape. Two quotes with one key name one thread.
func NormalizeName(s string) string {
	fields := strings.Fields(s)
	joined := strings.Join(fields, " ")
	trimmed := strings.TrimFunc(joined, func(r rune) bool {
		return unicode.IsSpace(r) || isEdgePunct(r)
	})
	return strings.ToLower(trimmed)
}

// isEdgePunct reports punctuation trimmed from quote edges. Letters,
// digits, and inner marks survive, so folded keys stay readable.
func isEdgePunct(r rune) bool {
	if unicode.IsLetter(r) || unicode.IsDigit(r) {
		return false
	}
	return unicode.IsPunct(r) || unicode.IsSymbol(r)
}

// tokensOf splits text into lowercase alphanumeric tokens. Punctuation
// becomes a separator, and empty tokens drop out. Both sides of a quote
// check run through it, so casing and commas never decide a match.
func tokensOf(s string) []string {
	var out []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			cur.WriteRune(r)
			continue
		}
		flush()
	}
	flush()
	return out
}

// QuoteInWords reports whether quote appears in words as one contiguous
// run. Comparison runs on folded tokens, so casing and punctuation never
// decide. A blank quote never matches, because nothing verifies against
// nothing.
func QuoteInWords(quote string, words []string) bool {
	return QuoteOffset(quote, words) >= 0
}

// QuoteOffset returns the index of the first word where quote starts, or
// minus one when quote never appears as a contiguous run. Thread links
// point at the returned offset, so playback starts where the words were
// said.
func QuoteOffset(quote string, words []string) int {
	want := tokensOf(quote)
	if len(want) == 0 || len(words) < len(want) {
		return -1
	}
	have := make([][]string, len(words))
	for i, w := range words {
		have[i] = tokensOf(w)
	}
	for start := 0; start < len(words); start++ {
		var flat []string
		for i := start; i < len(words) && len(flat) < len(want); i++ {
			flat = append(flat, have[i]...)
		}
		if len(flat) < len(want) {
			break
		}
		match := true
		for i := range want {
			if flat[i] != want[i] {
				match = false
				break
			}
		}
		if match {
			return start
		}
	}
	return -1
}
