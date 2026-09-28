package transcript_test

import (
	"testing"

	"github.com/nrynss/reprise/internal/assemblyai"
	"github.com/nrynss/reprise/internal/transcript"
)

// TestReplaceKeepsTheSpeaker merges one host reply beside one guest line,
// stores the timeline, and requires the load to name each speaker. A host
// word never reads as the guest. An insert that drops the role leaves both
// speakers empty and fails this test.
func TestReplaceKeepsTheSpeaker(t *testing.T) {
	t.Parallel()
	db := openDiary(t)
	addOwner(t, db, "owner-a")
	addEpisode(t, db, "ep-1", "owner-a", 1, "draft")
	user := []assemblyai.Word{{Text: "hi", StartMs: 200, EndMs: 300, Confidence: 0.99}}
	host := []transcript.HostReply{{
		StartMs: 0,
		Words:   []transcript.HostWord{{Text: "hello", StartMs: 0, EndMs: 100}},
	}}
	merged, err := transcript.Merge(user, host, transcript.Offsets{})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if err := transcript.Replace(t.Context(), db, "owner-a", "ep-1", merged); err != nil {
		t.Fatalf("replace: %v", err)
	}
	stored, err := transcript.Load(t.Context(), db, "ep-1")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(stored) != 2 || stored[0].Text != "hello" || stored[0].Role != transcript.RoleHost {
		t.Fatalf("stored = %+v, want hello as the host first", stored)
	}
	if stored[1].Text != "hi" || stored[1].Role != transcript.RoleUser {
		t.Fatalf("stored = %+v, want hi as the guest second", stored)
	}
	var first, second string
	if err := db.QueryRowContext(t.Context(),
		"SELECT speaker FROM words WHERE episode_id = 'ep-1' AND text = 'hello'").Scan(&first); err != nil {
		t.Fatalf("read hello speaker: %v", err)
	}
	if err := db.QueryRowContext(t.Context(),
		"SELECT speaker FROM words WHERE episode_id = 'ep-1' AND text = 'hi'").Scan(&second); err != nil {
		t.Fatalf("read hi speaker: %v", err)
	}
	if first != transcript.RoleHost || second != transcript.RoleUser {
		t.Fatalf("speakers = %q and %q, want host and user", first, second)
	}
}
