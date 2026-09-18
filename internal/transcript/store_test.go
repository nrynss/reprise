package transcript_test

import (
	"errors"
	"testing"

	"github.com/nrynss/reprise/internal/transcript"
)

// TestReplaceStoresEditWords pins the stored rows with source edit in
// timeline order.
func TestReplaceStoresEditWords(t *testing.T) {
	t.Parallel()
	db := openDiary(t)
	addOwner(t, db, "owner-a")
	addEpisode(t, db, "ep-1", "owner-a", 1, "draft")
	words := []transcript.Word{
		{Text: "last", StartMs: 900, EndMs: 1000, Role: transcript.RoleUser},
		{Text: "first", StartMs: 0, EndMs: 100, Role: transcript.RoleUser},
	}
	if err := transcript.Replace(t.Context(), db, "owner-a", "ep-1", words); err != nil {
		t.Fatalf("replace: %v", err)
	}
	stored, err := transcript.Load(t.Context(), db, "ep-1")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(stored) != 2 || stored[0].Text != "first" || stored[1].Text != "last" {
		t.Fatalf("stored = %+v, want timeline order", stored)
	}
	if stored[0].StartMs != 0 || stored[0].EndMs != 100 || stored[1].StartMs != 900 {
		t.Fatalf("stored spans = %+v, want the merged spans", stored)
	}
	for _, w := range stored {
		if w.ID == "" {
			t.Fatalf("stored word %+v carries no id", w)
		}
	}
	var source string
	if err := db.QueryRowContext(t.Context(),
		"SELECT DISTINCT source FROM words WHERE episode_id = 'ep-1'").Scan(&source); err != nil {
		t.Fatalf("read source: %v", err)
	}
	if source != transcript.SourceEdit {
		t.Fatalf("source = %q, want edit", source)
	}
}

// TestReplaceRerunWritesNoDuplicates replaces twice and checks the second
// run swaps the rows instead of doubling them.
func TestReplaceRerunWritesNoDuplicates(t *testing.T) {
	t.Parallel()
	db := openDiary(t)
	addOwner(t, db, "owner-a")
	addEpisode(t, db, "ep-1", "owner-a", 1, "draft")
	first := []transcript.Word{{Text: "one", StartMs: 0, EndMs: 50, Role: transcript.RoleUser}}
	second := []transcript.Word{{Text: "two", StartMs: 60, EndMs: 120, Role: transcript.RoleHost}}
	if err := transcript.Replace(t.Context(), db, "owner-a", "ep-1", first); err != nil {
		t.Fatalf("replace first: %v", err)
	}
	if err := transcript.Replace(t.Context(), db, "owner-a", "ep-1", second); err != nil {
		t.Fatalf("replace second: %v", err)
	}
	stored, err := transcript.Load(t.Context(), db, "ep-1")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(stored) != 1 || stored[0].Text != "two" {
		t.Fatalf("stored = %+v, want only the second run", stored)
	}
}

// TestReplaceRejectsBadRole checks an unknown role fails with the sentinel.
func TestReplaceRejectsBadRole(t *testing.T) {
	t.Parallel()
	db := openDiary(t)
	words := []transcript.Word{{Text: "x", StartMs: 0, EndMs: 10, Role: "robot"}}
	if err := transcript.Replace(t.Context(), db, "owner-a", "ep-1", words); !errors.Is(err, transcript.ErrInvalid) {
		t.Fatalf("replace error = %v, want ErrInvalid", err)
	}
}

// TestReplaceRejectsBadInput checks the sentinel on a nil database and an
// empty episode.
func TestReplaceRejectsBadInput(t *testing.T) {
	t.Parallel()
	db := openDiary(t)
	words := []transcript.Word{{Text: "x", StartMs: 0, EndMs: 10, Role: transcript.RoleUser}}
	if err := transcript.Replace(t.Context(), nil, "owner-a", "ep-1", words); !errors.Is(err, transcript.ErrInvalid) {
		t.Fatalf("replace nil database error = %v, want ErrInvalid", err)
	}
	if err := transcript.Replace(t.Context(), db, "owner-a", "", words); !errors.Is(err, transcript.ErrInvalid) {
		t.Fatalf("replace empty episode error = %v, want ErrInvalid", err)
	}
}
