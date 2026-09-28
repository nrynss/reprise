package episode_test

import (
	"testing"

	"github.com/nrynss/reprise/internal/episode"
	"github.com/nrynss/reprise/internal/transcript"
)

// TestEditWordsKeepTheSpeaker stores one host word beside one guest word
// through the transcript store and requires playback to name each speaker.
// A host word never reads as the guest. An insert that drops the role
// leaves both speakers empty and fails this test.
func TestEditWordsKeepTheSpeaker(t *testing.T) {
	t.Parallel()
	db := openDatabase(t)
	plantEpisode(t, db, "ep-1", 1, episode.StateDraft)
	words := []transcript.Word{
		{Text: "hello", StartMs: 0, EndMs: 100, Role: transcript.RoleHost},
		{Text: "hi", StartMs: 200, EndMs: 300, Role: transcript.RoleUser},
	}
	if err := transcript.Replace(t.Context(), db.Writer(), "owner-1", "ep-1", words); err != nil {
		t.Fatalf("replace: %v", err)
	}
	svc, err := episode.NewService(episode.Config{DB: db})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	got, err := svc.EditWords(t.Context(), "owner-1", "ep-1")
	if err != nil {
		t.Fatalf("edit words: %v", err)
	}
	if len(got) != 2 || got[0].Text != "hello" || got[0].Speaker != transcript.RoleHost {
		t.Fatalf("words = %+v, want hello as the host first", got)
	}
	if got[1].Text != "hi" || got[1].Speaker != transcript.RoleUser {
		t.Fatalf("words = %+v, want hi as the guest second", got)
	}
}
