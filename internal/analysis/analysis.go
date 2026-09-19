// Package analysis turns one finished render into chapters, notes, and
// memory.
//
// The pass batch-transcribes the rendered file, never the raw take, so
// every timestamp describes the episode people hear. Words land in the
// words table under their own source, entities and key phrases become
// mention rows with rendered word offsets, and chapters plus the summary
// land in the analyses row as JSON. The provider copy is deleted once
// stored and confirmed gone with a fresh fetch.
//
// The pass talks to the provider through two narrow seams. Transcriber
// carries the batch exchange and Chapterer carries the chapter call. Tests
// bind the scripted doubles in scripted.go, so the whole pass runs
// offline. The production wiring binds both seams to the provider
// adapter, which owns every wire shape and every HTTP call.
package analysis

import (
	"errors"
)

// SourceRendered marks words built from the rendered file. The edit pass
// stores its own words under a different source, so the two passes never
// mix.
const SourceRendered = "rendered"

// MinChapterMs is the shortest chapter the pass keeps. Shorter spans
// merge into their neighbour, so seeking never lands on a sliver.
const MinChapterMs = 10000

// WantChapters is the chapter count a long enough episode carries. Short
// episodes carry fewer, because padding would invent structure.
const WantChapters = 3

// Sentinel errors. Every failure path this package produces wraps one of
// these, so callers branch with errors.Is.
var (
	// ErrInvalid reports a call with an empty id, a nil dependency, or
	// empty audio.
	ErrInvalid = errors.New("analysis: invalid argument")
	// ErrOrder reports a provider word ending before it starts.
	ErrOrder = errors.New("analysis: word ends before it starts")
	// ErrNoRender reports a render locator that returns no audio.
	ErrNoRender = errors.New("analysis: render audio missing")
	// ErrChapters reports chapter drafts the pass cannot shape into the
	// episode chapters, such as none at all or one without a title.
	ErrChapters = errors.New("analysis: unusable chapter drafts")
	// ErrDeleteUnconfirmed reports a provider copy that stays readable
	// after the delete call, so spend cannot settle while a copy lingers.
	ErrDeleteUnconfirmed = errors.New("analysis: provider copy still readable")
)
