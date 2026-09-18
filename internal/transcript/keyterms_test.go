package transcript_test

import (
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/nrynss/reprise/internal/transcript"
)

// TestKeytermsBoostsMentionsRecentFirst seeds three names and pins the
// recency order with the most recent wording kept.
func TestKeytermsBoostsMentionsRecentFirst(t *testing.T) {
	t.Parallel()
	db := openDiary(t)
	addOwner(t, db, "owner-a")
	addEpisode(t, db, "ep-1", "owner-a", 1, "draft")
	addEpisode(t, db, "ep-2", "owner-a", 2, "draft")
	addMention(t, db, "m-1", "owner-a", "ep-1", "Mara")
	addMention(t, db, "m-2", "owner-a", "ep-1", "Quilby")
	addMention(t, db, "m-3", "owner-a", "ep-2", "Lisbon")
	terms, err := transcript.Keyterms(t.Context(), db, "owner-a")
	if err != nil {
		t.Fatalf("keyterms: %v", err)
	}
	if !slices.Equal(terms, []string{"Lisbon", "Quilby", "Mara"}) {
		t.Fatalf("keyterms = %q, want recent first", terms)
	}
}

// TestKeytermsDedupesAndSkipsJunk writes one name twice, a blank quote, and
// a phrase over the provider word cap. One clean entry survives.
func TestKeytermsDedupesAndSkipsJunk(t *testing.T) {
	t.Parallel()
	db := openDiary(t)
	addOwner(t, db, "owner-b")
	addEpisode(t, db, "ep-1", "owner-b", 1, "draft")
	addMention(t, db, "m-1", "owner-b", "ep-1", "maya")
	addMention(t, db, "m-2", "owner-b", "ep-1", "Maya")
	addMention(t, db, "m-3", "owner-b", "ep-1", "   ")
	addMention(t, db, "m-4", "owner-b", "ep-1", "one two three four five six seven")
	terms, err := transcript.Keyterms(t.Context(), db, "owner-b")
	if err != nil {
		t.Fatalf("keyterms: %v", err)
	}
	if !slices.Equal(terms, []string{"Maya"}) {
		t.Fatalf("keyterms = %q, want one clean entry", terms)
	}
}

// TestKeytermsCapsAtOneHundred seeds more names than the request carries
// and checks the list stops at the cap.
func TestKeytermsCapsAtOneHundred(t *testing.T) {
	t.Parallel()
	db := openDiary(t)
	addOwner(t, db, "owner-c")
	addEpisode(t, db, "ep-1", "owner-c", 1, "draft")
	for i := range 120 {
		addMention(t, db, fmt.Sprintf("m-%d", i), "owner-c", "ep-1", fmt.Sprintf("Guest %d", i))
	}
	terms, err := transcript.Keyterms(t.Context(), db, "owner-c")
	if err != nil {
		t.Fatalf("keyterms: %v", err)
	}
	if len(terms) != transcript.MaxKeyterms {
		t.Fatalf("keyterms holds %d names, want %d", len(terms), transcript.MaxKeyterms)
	}
}

// TestKeytermsRejectsBadInput checks the sentinel on a nil database and an
// empty owner.
func TestKeytermsRejectsBadInput(t *testing.T) {
	t.Parallel()
	db := openDiary(t)
	if _, err := transcript.Keyterms(t.Context(), nil, "owner-a"); !errors.Is(err, transcript.ErrInvalid) {
		t.Fatalf("keyterms nil database error = %v, want ErrInvalid", err)
	}
	if _, err := transcript.Keyterms(t.Context(), db, ""); !errors.Is(err, transcript.ErrInvalid) {
		t.Fatalf("keyterms empty owner error = %v, want ErrInvalid", err)
	}
}
