package export

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	"github.com/nrynss/keel/caption"
)

// ToCaptionWords validates rendered words and converts them for the
// library captioner. Words must arrive ordered by start with no inverted
// span, because a cue built on shuffled timings would mislabel the
// audio. Times convert without floats, so a cue boundary never drifts.
func ToCaptionWords(words []Word) ([]caption.Word, error) {
	if len(words) == 0 {
		return nil, fmt.Errorf("export: convert %d words: %w", len(words), ErrNoWords)
	}
	out := make([]caption.Word, 0, len(words))
	for i, w := range words {
		if strings.TrimSpace(w.Text) == "" {
			return nil, fmt.Errorf("export: convert word %d: %w: empty text", i, ErrInvalid)
		}
		if w.StartMs < 0 || w.EndMs < w.StartMs {
			return nil, fmt.Errorf("export: convert word %d %q: %w", i, w.Text, ErrOrder)
		}
		if i > 0 && time.Duration(w.StartMs)*time.Millisecond < out[i-1].Start {
			return nil, fmt.Errorf("export: convert word %d %q: %w", i, w.Text, ErrOrder)
		}
		out = append(out, caption.Word{
			Text:  w.Text,
			Start: time.Duration(w.StartMs) * time.Millisecond,
			End:   time.Duration(w.EndMs) * time.Millisecond,
		})
	}
	return out, nil
}

// SRT renders rendered words as SubRip text. Cues group through the
// library captioner, so this package never formats a cue line itself.
func SRT(words []Word) (string, error) {
	captionWords, err := ToCaptionWords(words)
	if err != nil {
		return "", err
	}
	cues := caption.Group(captionWords, caption.Config{})
	if len(cues) == 0 {
		return "", fmt.Errorf("export: captions: %w", ErrNoWords)
	}
	var buf bytes.Buffer
	if err := caption.SRT(&buf, cues); err != nil {
		return "", fmt.Errorf("export: captions: %w", err)
	}
	return buf.String(), nil
}

// WebVTT renders rendered words as WebVTT text. Cues group through the
// same library call as SRT, so both caption files describe the same
// timings.
func WebVTT(words []Word) (string, error) {
	captionWords, err := ToCaptionWords(words)
	if err != nil {
		return "", err
	}
	cues := caption.Group(captionWords, caption.Config{})
	if len(cues) == 0 {
		return "", fmt.Errorf("export: captions: %w", ErrNoWords)
	}
	var buf bytes.Buffer
	if err := caption.WebVTT(&buf, cues); err != nil {
		return "", fmt.Errorf("export: captions: %w", err)
	}
	return buf.String(), nil
}
