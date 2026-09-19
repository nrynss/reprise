// Package memory derives the cross episode index from stored rows.
//
// Every thread the gallery shows and every name the host speaks comes from
// a diary row. The model only phrases what the queries return. People and
// places group by normalised quote. Commitments enter as mention rows only
// when their exact quote sits in the episode words. A later doing mention
// closes a commitment through a resolution row that links the two stored
// quotes. Key phrases in three or more episodes with no close on record
// circle. Keyterms rank distinct names by count, then by recency.
//
// The model marks candidates through the Model seam. Tests bind a scripted
// answer, so the whole index runs offline. Production binds the seam to
// the provider package, which owns every wire shape and every paid call.
// The caller reserves budget before each marking call and persists the raw
// answer on receipt, because provider output persists and paid calls
// reserve first.
package memory

import (
	"errors"
)

// Sentinel errors. Every failure path this package produces wraps one of
// these, so callers branch with errors.Is.
var (
	// ErrInvalid reports a call with an empty id, a nil dependency, or a
	// blank quote where a real one belongs.
	ErrInvalid = errors.New("memory: invalid argument")
	// ErrModel reports a model call that failed, answered empty, or
	// answered JSON the pass cannot use. The wrapped error carries the
	// cause. Stored rows stay untouched.
	ErrModel = errors.New("memory: model failed")
	// ErrNoWords reports an episode with no rendered words. Marking needs
	// the transcript, and verification needs the word list.
	ErrNoWords = errors.New("memory: episode has no rendered words")
	// ErrNotFound reports an unknown commitment id, or one owned by
	// someone else. Resolution names a stored row, never a guess.
	ErrNotFound = errors.New("memory: commitment not found")
)

// MaxKeyterms caps the names one ranking returns. The live session
// forwards at most one hundred keyterms, so the index never returns more.
const MaxKeyterms = 100

// CircledMinEpisodes floors the episode span of a circled topic. A key
// phrase in fewer episodes recurs, but it has not circled yet.
const CircledMinEpisodes = 3
