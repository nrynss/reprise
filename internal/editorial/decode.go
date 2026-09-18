package editorial

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ErrInvalid reports a run with an empty id, a nil store, a nil model
// client, or empty audio.
var ErrInvalid = errors.New("editorial: invalid argument")

// ErrModel reports a model call that failed, answered empty, or answered
// JSON the pass cannot use. The wrapped error carries the cause. The run
// still leaves a renderable draft, so this sentinel never aborts the
// episode.
var ErrModel = errors.New("editorial: model failed")

// span is one word range by timeline offsets. Both bounds are indexes into
// the timeline the prompt numbered.
type span struct {
	// StartWord is the offset of the first word.
	StartWord int `json:"start_word"`
	// EndWord is the offset of the last word.
	EndWord int `json:"end_word"`
	// Reason says in one line why this span opens. Empty on spans the
	// schema leaves unexplained.
	Reason string `json:"reason"`
}

// cut is one proposed removal with its one line reason.
type cut struct {
	// StartWord is the offset of the first word to remove.
	StartWord int `json:"start_word"`
	// EndWord is the offset of the last word to remove.
	EndWord int `json:"end_word"`
	// Reason says in one line why this span goes.
	Reason string `json:"reason"`
}

// callback is the unresolved thing to open next time, with its quote and
// the span the quote came from.
type callback struct {
	// StartWord is the offset of the first quoted word.
	StartWord int `json:"start_word"`
	// EndWord is the offset of the last quoted word.
	EndWord int `json:"end_word"`
	// Quote carries the speaker's own words, quoted exactly.
	Quote string `json:"quote"`
	// Text names the unresolved thing in one line.
	Text string `json:"text"`
}

// answer is the model response decoded from JSON. Field pointers tell a
// missing object apart from an empty one, so an absent cold open drops
// the cold open without failing the pass.
type answer struct {
	// ColdOpen is the opening span, or nil when the model sent none.
	ColdOpen *span `json:"cold_open"`
	// Cuts holds every proposed removal.
	Cuts []cut `json:"cuts"`
	// Title names the episode, short and specific.
	Title string `json:"title"`
	// ShowNotes carries a few sentences in the speaker's own words.
	ShowNotes string `json:"show_notes"`
	// Callback is the unresolved thing, or nil when the model sent none.
	Callback *callback `json:"callback"`
}

// decode parses the model answer. Unknown fields fail the parse, so a
// drifted schema cannot silently slide past. A blank answer or one that
// is not JSON fails with ErrModel.
func decode(raw string) (answer, error) {
	var out answer
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&out); err != nil {
		return answer{}, fmt.Errorf("editorial: decode: %w: %w", ErrModel, err)
	}
	return out, nil
}
