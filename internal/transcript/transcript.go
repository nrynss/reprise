// Package transcript builds the word timeline the editor cuts against.
//
// The live session carries no user word timings, so the user stem is
// batch-transcribed after recording. Host words reuse the live reply
// timings. Merge places both sides on one episode clock, and Replace stores
// the result for the editor. Run drives the whole pass behind a budget.
package transcript

import (
	"errors"
	"fmt"

	"github.com/nrynss/reprise/internal/assemblyai"
)

// SourceEdit marks words built for editing. Analysis stores its own words
// under a different source, so the two passes never mix.
const SourceEdit = "edit"

// RoleUser marks a word spoken by the guest.
const RoleUser = "user"

// RoleHost marks a word spoken by the host.
const RoleHost = "host"

// ErrInvalid reports a call with an empty id, a nil store, or empty audio.
var ErrInvalid = errors.New("transcript: invalid argument")

// ErrOrder reports a word ending before it starts after shifting.
var ErrOrder = errors.New("transcript: word ends before it starts")

// ErrDeleteUnconfirmed reports a provider copy that stays readable after
// the delete call, so spend cannot settle while a copy lingers.
var ErrDeleteUnconfirmed = errors.New("transcript: provider copy still readable")

// Word is one timeline word on the episode clock.
type Word struct {
	// ID identifies the row. Merge leaves it empty and Replace mints it.
	ID string
	// Text is the word as heard, with its original casing.
	Text string
	// StartMs is the word start in milliseconds on the episode clock.
	StartMs int64
	// EndMs is the word end in milliseconds on the episode clock.
	EndMs int64
	// Role names the speaker, either RoleUser or RoleHost.
	Role string
}

// HostWord is one host word with offsets inside its reply audio.
type HostWord struct {
	// Text is the word as the live transcript heard it.
	Text string
	// StartMs is the word start in milliseconds from the reply start.
	StartMs int64
	// EndMs is the word end in milliseconds from the reply start.
	EndMs int64
}

// HostReply is one host reply with its start on the host stem.
type HostReply struct {
	// StartMs is the reply start in milliseconds from the stem start.
	StartMs int64
	// Words holds the reply words on the reply clock.
	Words []HostWord
}

// Offsets carries each stem shift to the episode clock. The alignment pass
// measures them once it lands. Callers pass zeros until then, which keeps
// each stem clock as the episode clock.
type Offsets struct {
	// UserMs shifts user stem times to the episode clock.
	UserMs int64
	// HostMs shifts host stem times to the episode clock.
	HostMs int64
}

// Merge shifts user words by the user offset and host words by their reply
// start plus the host offset, then sorts by start. Ties break with the user
// first, so the order stays deterministic across runs. Merge is pure, so
// tests assert exact outputs on synthetic samples.
func Merge(user []assemblyai.Word, host []HostReply, off Offsets) ([]Word, error) {
	out := make([]Word, 0, len(user))
	for _, w := range user {
		shifted, err := shift(w.Text, w.StartMs+off.UserMs, w.EndMs+off.UserMs, RoleUser)
		if err != nil {
			return nil, err
		}
		out = append(out, shifted)
	}
	for _, reply := range host {
		for _, w := range reply.Words {
			shifted, err := shift(w.Text, reply.StartMs+w.StartMs+off.HostMs, reply.StartMs+w.EndMs+off.HostMs, RoleHost)
			if err != nil {
				return nil, err
			}
			out = append(out, shifted)
		}
	}
	sortWords(out)
	return out, nil
}

// shift builds one timeline word and rejects an inverted span.
func shift(text string, startMs, endMs int64, role string) (Word, error) {
	if endMs < startMs {
		return Word{}, fmt.Errorf("transcript: shift %q %d to %d: %w", text, startMs, endMs, ErrOrder)
	}
	return Word{Text: text, StartMs: startMs, EndMs: endMs, Role: role}, nil
}

// sortWords orders words by start, then by role with the user first, then
// by end. The full key keeps ties deterministic.
func sortWords(words []Word) {
	for i := 1; i < len(words); i++ {
		for j := i; j > 0 && before(words[j], words[j-1]); j-- {
			words[j], words[j-1] = words[j-1], words[j]
		}
	}
}

// before reports whether a sorts ahead of b on the timeline order.
func before(a, b Word) bool {
	if a.StartMs != b.StartMs {
		return a.StartMs < b.StartMs
	}
	if a.Role != b.Role {
		return a.Role == RoleUser
	}
	return a.EndMs < b.EndMs
}
