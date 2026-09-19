package memory_test

import (
	"strings"
	"testing"

	"github.com/nrynss/reprise/internal/host"
	"github.com/nrynss/reprise/internal/memory"
)

// TestSelectPrefersOpenCommitment pins the done case. Episode five
// opens on the episode one dread with its number, through the same
// greeting builder the live session serves.
func TestSelectPrefersOpenCommitment(t *testing.T) {
	t.Parallel()
	db := openIndex(t)
	addOwner(t, db, "owner-cb")
	addEpisode(t, db, "owner-cb-ep1", "owner-cb", 1)
	addEpisode(t, db, "owner-cb-ep2", "owner-cb", 2)
	addEpisode(t, db, "owner-cb-ep3", "owner-cb", 3)
	addEpisode(t, db, "owner-cb-ep4", "owner-cb", 4)
	dread := "I am dreading the conversation with my sister"
	addWords(t, db, "owner-cb", "owner-cb-ep1", strings.Fields("I am dreading the conversation with my sister today"))
	addMention(t, db, "owner-cb-c1", "owner-cb", "owner-cb-ep1", "commitment", 1, dread)
	addMention(t, db, "owner-cb-n1", "owner-cb", "owner-cb-ep1", "person_name", 0, "Maya")
	addMention(t, db, "owner-cb-k1", "owner-cb", "owner-cb-ep1", "keyphrase", 0, "the allotment")
	addMention(t, db, "owner-cb-n2", "owner-cb", "owner-cb-ep2", "person_name", 0, "Maya")
	addMention(t, db, "owner-cb-k2", "owner-cb", "owner-cb-ep2", "keyphrase", 0, "the allotment")
	addMention(t, db, "owner-cb-k3", "owner-cb", "owner-cb-ep3", "keyphrase", 0, "the allotment")

	pick, err := memory.Select(t.Context(), db, "owner-cb", "owner-cb-ep4")
	if err != nil {
		t.Fatalf("select callback: %v", err)
	}
	if pick == nil {
		t.Fatal("select returns nothing on a season with an open commitment")
	}
	if pick.Kind != memory.CallbackCommitment {
		t.Fatalf("pick kind = %q, want commitment", pick.Kind)
	}
	if pick.EpisodeNumber != 1 || pick.Quote != dread {
		t.Fatalf("pick = %+v, want the episode one dread", pick)
	}
	if pick.MentionID != "owner-cb-c1" {
		t.Fatalf("pick mention = %q, want the stored commitment row", pick.MentionID)
	}
	if pick.Planted {
		t.Fatal("pick claims a planted row the season never planted")
	}

	greeting := host.Build(host.Input{
		Callback: &host.Callback{
			ID: pick.CallbackID,
			Mention: host.Mention{
				ID:            pick.MentionID,
				EpisodeNumber: pick.EpisodeNumber,
				Kind:          "commitment",
				Quote:         pick.Quote,
			},
		},
	})
	if !strings.Contains(greeting.Greeting, "episode 1") {
		t.Fatalf("greeting = %q, want the episode one number", greeting.Greeting)
	}
	if !strings.Contains(greeting.Greeting, dread) {
		t.Fatalf("greeting = %q, want the dread quote", greeting.Greeting)
	}
}

// TestSelectHonorsAgreeingPlantedRow pins the editorial preference. A
// planted row quoting the open commitment wins by id, and no second
// row lands beside it.
func TestSelectHonorsAgreeingPlantedRow(t *testing.T) {
	t.Parallel()
	db := openIndex(t)
	addOwner(t, db, "owner-cb")
	addEpisode(t, db, "owner-cb-ep1", "owner-cb", 1)
	addEpisode(t, db, "owner-cb-ep2", "owner-cb", 2)
	dread := "I am dreading the conversation with my sister"
	addWords(t, db, "owner-cb", "owner-cb-ep1", strings.Fields("I am dreading the conversation with my sister today"))
	addMention(t, db, "owner-cb-c1", "owner-cb", "owner-cb-ep1", "commitment", 1, dread)
	addMention(t, db, "owner-cb-p1", "owner-cb", "owner-cb-ep2", "callback", 1, dread)
	mustExec(t, db, "INSERT INTO callbacks (id, owner_id, episode_id, mention_id, used) VALUES ('owner-cb-plant', 'owner-cb', 'owner-cb-ep2', 'owner-cb-p1', 0)")

	pick, err := memory.Select(t.Context(), db, "owner-cb", "owner-cb-ep2")
	if err != nil {
		t.Fatalf("select callback: %v", err)
	}
	if pick == nil || !pick.Planted {
		t.Fatalf("pick = %+v, want the planted row honored", pick)
	}
	if pick.CallbackID != "owner-cb-plant" {
		t.Fatalf("pick callback = %q, want the planted row id", pick.CallbackID)
	}
	var n int
	if err := db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM callbacks WHERE owner_id = 'owner-cb'").Scan(&n); err != nil {
		t.Fatalf("count callbacks: %v", err)
	}
	if n != 1 {
		t.Fatalf("callbacks = %d, want the planted row only", n)
	}
}

// TestSelectIgnoresDisagreeingPlantedRow pins the data check. A planted
// row naming something the index never held loses to the commitment,
// and the stale planted row leaves the unused list.
func TestSelectIgnoresDisagreeingPlantedRow(t *testing.T) {
	t.Parallel()
	db := openIndex(t)
	addOwner(t, db, "owner-cb")
	addEpisode(t, db, "owner-cb-ep1", "owner-cb", 1)
	addEpisode(t, db, "owner-cb-ep2", "owner-cb", 2)
	dread := "I am dreading the conversation with my sister"
	addWords(t, db, "owner-cb", "owner-cb-ep1", strings.Fields("I am dreading the conversation with my sister today"))
	addWords(t, db, "owner-cb", "owner-cb-ep2", strings.Fields("we sailed to mars at dawn"))
	addMention(t, db, "owner-cb-c1", "owner-cb", "owner-cb-ep1", "commitment", 1, dread)
	addMention(t, db, "owner-cb-p1", "owner-cb", "owner-cb-ep2", "callback", 1, "we sailed to mars")
	mustExec(t, db, "INSERT INTO callbacks (id, owner_id, episode_id, mention_id, used) VALUES ('owner-cb-stale', 'owner-cb', 'owner-cb-ep2', 'owner-cb-p1', 0)")

	pick, err := memory.Select(t.Context(), db, "owner-cb", "owner-cb-ep2")
	if err != nil {
		t.Fatalf("select callback: %v", err)
	}
	if pick == nil || pick.Planted || pick.Quote != dread {
		t.Fatalf("pick = %+v, want the data commitment over the stale plant", pick)
	}
}

// TestSelectNeverRepeatsLastChoice pins the no repeat rule. After the
// commitment opens once and marks used, the next pick names someone
// else.
func TestSelectNeverRepeatsLastChoice(t *testing.T) {
	t.Parallel()
	db := openIndex(t)
	addOwner(t, db, "owner-cb")
	addEpisode(t, db, "owner-cb-ep1", "owner-cb", 1)
	addEpisode(t, db, "owner-cb-ep2", "owner-cb", 2)
	addEpisode(t, db, "owner-cb-ep3", "owner-cb", 3)
	dread := "I am dreading the conversation with my sister"
	addWords(t, db, "owner-cb", "owner-cb-ep1", strings.Fields("I am dreading the conversation with my sister today"))
	addMention(t, db, "owner-cb-c1", "owner-cb", "owner-cb-ep1", "commitment", 1, dread)
	addMention(t, db, "owner-cb-n1", "owner-cb", "owner-cb-ep1", "person_name", 0, "Maya")
	addMention(t, db, "owner-cb-n2", "owner-cb", "owner-cb-ep2", "person_name", 0, "Maya")

	first, err := memory.Select(t.Context(), db, "owner-cb", "owner-cb-ep2")
	if err != nil {
		t.Fatalf("first select: %v", err)
	}
	if first == nil || first.Quote != dread {
		t.Fatalf("first pick = %+v, want the dread", first)
	}
	if err := memory.MarkUsed(t.Context(), db, "owner-cb", first.CallbackID); err != nil {
		t.Fatalf("mark used: %v", err)
	}

	second, err := memory.Select(t.Context(), db, "owner-cb", "owner-cb-ep3")
	if err != nil {
		t.Fatalf("second select: %v", err)
	}
	if second == nil {
		t.Fatal("second select returns nothing while Maya recurs")
	}
	if memory.NormalizeName(second.Quote) == memory.NormalizeName(dread) {
		t.Fatalf("second pick = %+v, want anything but the last opening", second)
	}
	if second.Kind != memory.CallbackPerson {
		t.Fatalf("second kind = %q, want the recurring person", second.Kind)
	}
}

// TestSelectFallsThroughTiers pins the priority order end to end. The
// commitment wins first, and after its close the recurring person
// wins over the circled topic.
func TestSelectFallsThroughTiers(t *testing.T) {
	t.Parallel()
	db := openIndex(t)
	addOwner(t, db, "owner-cb")
	addEpisode(t, db, "owner-cb-ep1", "owner-cb", 1)
	addEpisode(t, db, "owner-cb-ep2", "owner-cb", 2)
	addEpisode(t, db, "owner-cb-ep3", "owner-cb", 3)
	addEpisode(t, db, "owner-cb-ep4", "owner-cb", 4)
	addWords(t, db, "owner-cb", "owner-cb-ep4", strings.Fields("I called Maya back today"))
	addMention(t, db, "owner-cb-c1", "owner-cb", "owner-cb-ep1", "commitment", 0, "I will call Maya back")
	addMention(t, db, "owner-cb-n1", "owner-cb", "owner-cb-ep1", "person_name", 0, "Maya")
	addMention(t, db, "owner-cb-n2", "owner-cb", "owner-cb-ep2", "person_name", 0, "Maya")
	addMention(t, db, "owner-cb-k1", "owner-cb", "owner-cb-ep1", "keyphrase", 0, "the allotment")
	addMention(t, db, "owner-cb-k2", "owner-cb", "owner-cb-ep2", "keyphrase", 0, "the allotment")
	addMention(t, db, "owner-cb-k3", "owner-cb", "owner-cb-ep3", "keyphrase", 0, "the allotment")

	pick, err := memory.Select(t.Context(), db, "owner-cb", "owner-cb-ep3")
	if err != nil {
		t.Fatalf("select callback: %v", err)
	}
	if pick == nil || pick.Kind != memory.CallbackCommitment {
		t.Fatalf("pick = %+v, want the open commitment first", pick)
	}
	if err := memory.MarkUsed(t.Context(), db, "owner-cb", pick.CallbackID); err != nil {
		t.Fatalf("mark used: %v", err)
	}

	if err := memory.StoreResolution(t.Context(), db, "owner-cb", "owner-cb-c1", "owner-cb-ep4", "called Maya back", 1); err != nil {
		t.Fatalf("store resolution: %v", err)
	}
	next, err := memory.Select(t.Context(), db, "owner-cb", "owner-cb-ep4")
	if err != nil {
		t.Fatalf("select after close: %v", err)
	}
	if next == nil || next.Kind != memory.CallbackPerson {
		t.Fatalf("next = %+v, want the recurring person after the close", next)
	}
}

// TestSelectReturnsNilOnEmptySeason pins the quiet path. A season with
// no threads stores nothing and reports no pick.
func TestSelectReturnsNilOnEmptySeason(t *testing.T) {
	t.Parallel()
	db := openIndex(t)
	addOwner(t, db, "owner-cb")
	addEpisode(t, db, "owner-cb-ep1", "owner-cb", 1)

	pick, err := memory.Select(t.Context(), db, "owner-cb", "owner-cb-ep1")
	if err != nil {
		t.Fatalf("select callback: %v", err)
	}
	if pick != nil {
		t.Fatalf("pick = %+v, want nil on an empty season", pick)
	}
	var n int
	if err := db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM callbacks").Scan(&n); err != nil {
		t.Fatalf("count callbacks: %v", err)
	}
	if n != 0 {
		t.Fatalf("callbacks = %d, want none stored", n)
	}
}

// TestMarkUsed pins the spoken flag. The flagged row leaves the unused
// list, and an unknown id fails without touching the table.
func TestMarkUsed(t *testing.T) {
	t.Parallel()
	db := openIndex(t)
	addOwner(t, db, "owner-cb")
	addEpisode(t, db, "owner-cb-ep1", "owner-cb", 1)
	addMention(t, db, "owner-cb-m1", "owner-cb", "owner-cb-ep1", "person_name", 0, "Maya")
	mustExec(t, db, "INSERT INTO callbacks (id, owner_id, episode_id, mention_id, used) VALUES ('owner-cb-1', 'owner-cb', 'owner-cb-ep1', 'owner-cb-m1', 0)")

	if err := memory.MarkUsed(t.Context(), db, "owner-cb", "owner-cb-1"); err != nil {
		t.Fatalf("mark used: %v", err)
	}
	var used int
	if err := db.QueryRowContext(t.Context(), "SELECT used FROM callbacks WHERE id = 'owner-cb-1'").Scan(&used); err != nil {
		t.Fatalf("read used flag: %v", err)
	}
	if used != 1 {
		t.Fatalf("used = %d, want 1 after the greeting", used)
	}
	if err := memory.MarkUsed(t.Context(), db, "owner-cb", "missing"); err == nil {
		t.Fatal("unknown callback marks used")
	}
}
