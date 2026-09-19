package retention_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nrynss/reprise/internal/retention"
)

// TestKeepSurvivesGuestExpiry pins the keep half. The kept episode moves
// to the owner with every row and blob, so the sweep that deletes the rest
// of the guest leaves the kept episode playing and its provider copies
// untouched.
func TestKeepSurvivesGuestExpiry(t *testing.T) {
	fx := openFixture(t)
	idle := testNow.Add(-testWindow).Add(-time.Hour)
	fx.seedUser(t, "owner-keeper", "owner", testNow)
	seeds := fx.seedGuest(t, "guest-gone", idle)
	kept, lost := seeds[0], seeds[1]

	if err := fx.svc.Keep(t.Context(), kept.episode, "owner-keeper"); err != nil {
		t.Fatalf("keep: %v", err)
	}

	var owner string
	var number int64
	if err := fx.db.Reader().QueryRowContext(t.Context(),
		"SELECT owner_id, number FROM episodes WHERE id = ?", kept.episode).Scan(&owner, &number); err != nil {
		t.Fatalf("read kept episode: %v", err)
	}
	if owner != "owner-keeper" {
		t.Fatalf("kept episode owner %s, want owner-keeper", owner)
	}
	if number != 1 {
		t.Fatalf("kept episode number %d, want 1", number)
	}
	for _, table := range contentTables {
		if table == "episodes" {
			continue
		}
		var total int
		if err := fx.db.Reader().QueryRowContext(t.Context(),
			"SELECT COUNT(*) FROM "+table+" WHERE episode_id = ? AND owner_id = ?",
			kept.episode, "owner-keeper").Scan(&total); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if total == 0 {
			t.Fatalf("table %s holds no kept rows, want the moved set", table)
		}
	}
	var mediaOwner string
	if err := fx.db.Reader().QueryRowContext(t.Context(),
		"SELECT owner FROM media WHERE id = ?", kept.opus).Scan(&mediaOwner); err != nil {
		t.Fatalf("read kept blob: %v", err)
	}
	if mediaOwner != "owner-keeper" {
		t.Fatalf("kept blob owner %s, want owner-keeper", mediaOwner)
	}

	jobID, err := fx.svc.Sweep(t.Context())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	fx.waitSweepDone(t, jobID)

	if got := fx.count(t, "episodes", "owner-keeper"); got != 1 {
		t.Fatalf("episodes holds %d kept rows, want 1", got)
	}
	for _, blob := range append(append([]string{}, kept.stems...), kept.opus, kept.aac, kept.stereo) {
		if _, err := os.Stat(filepath.Join(fx.mediaDir, blob)); err != nil {
			t.Fatalf("kept blob %s missing (err %v)", blob, err)
		}
	}
	if _, err := os.Stat(filepath.Join(fx.coverDir, kept.coverFile)); err != nil {
		t.Fatalf("kept cover missing (err %v)", err)
	}
	if fx.sessions.ended(kept.session) {
		t.Fatalf("kept provider session ended, want it untouched")
	}
	if fx.transcript.deletedID(kept.batch) {
		t.Fatalf("kept provider transcript deleted, want it untouched")
	}

	if got := fx.count(t, "episodes", "guest-gone"); got != 0 {
		t.Fatalf("episodes holds %d guest rows, want none", got)
	}
	if !fx.sessions.ended(lost.session) {
		t.Fatalf("lost provider session %s never ended", lost.session)
	}
	if got := fx.count(t, "users", "guest-gone"); got != 0 {
		t.Fatalf("users holds %d guest rows, want none", got)
	}
	if got := fx.count(t, "users", "owner-keeper"); got != 1 {
		t.Fatalf("users holds %d keeper rows, want 1", got)
	}
}

// TestKeepTakesNextNumber pins the numbering. A keeper who already owns
// episodes keeps the next free number, so the unique pair never collides.
func TestKeepTakesNextNumber(t *testing.T) {
	fx := openFixture(t)
	fx.seedUser(t, "owner-keeper", "owner", testNow)
	fx.seedEpisode(t, "owner-keeper", 1)
	fx.seedEpisode(t, "owner-keeper", 2)
	seeds := fx.seedGuest(t, "guest-gone", testNow)

	if err := fx.svc.Keep(t.Context(), seeds[0].episode, "owner-keeper"); err != nil {
		t.Fatalf("keep: %v", err)
	}
	var number int64
	if err := fx.db.Reader().QueryRowContext(t.Context(),
		"SELECT number FROM episodes WHERE id = ?", seeds[0].episode).Scan(&number); err != nil {
		t.Fatalf("read kept number: %v", err)
	}
	if number != 3 {
		t.Fatalf("kept episode number %d, want 3", number)
	}
}

// TestKeepIdempotent pins the repeated keep. Keeping an episode the
// keeper already owns succeeds without renumbering it.
func TestKeepIdempotent(t *testing.T) {
	fx := openFixture(t)
	fx.seedUser(t, "owner-keeper", "owner", testNow)
	seeds := fx.seedGuest(t, "guest-gone", testNow)

	if err := fx.svc.Keep(t.Context(), seeds[0].episode, "owner-keeper"); err != nil {
		t.Fatalf("keep: %v", err)
	}
	if err := fx.svc.Keep(t.Context(), seeds[0].episode, "owner-keeper"); err != nil {
		t.Fatalf("repeat keep: %v", err)
	}
	var number int64
	if err := fx.db.Reader().QueryRowContext(t.Context(),
		"SELECT number FROM episodes WHERE id = ?", seeds[0].episode).Scan(&number); err != nil {
		t.Fatalf("read kept number: %v", err)
	}
	if number != 1 {
		t.Fatalf("kept episode number %d, want 1", number)
	}
}

// TestKeepUnknown pins the missing rows. An unknown episode and an
// unknown keeper both report not found, and empty ids report invalid.
func TestKeepUnknown(t *testing.T) {
	fx := openFixture(t)
	fx.seedUser(t, "owner-keeper", "owner", testNow)
	fx.seedGuest(t, "guest-gone", testNow)

	if err := fx.svc.Keep(t.Context(), "no-such-episode", "owner-keeper"); !errors.Is(err, retention.ErrNotFound) {
		t.Fatalf("keep unknown episode err %v, want not found", err)
	}
	if err := fx.svc.Keep(t.Context(), fx.seeds["guest-gone"][0].episode, "no-such-keeper"); !errors.Is(err, retention.ErrNotFound) {
		t.Fatalf("keep unknown keeper err %v, want not found", err)
	}
	if err := fx.svc.Keep(t.Context(), "", "owner-keeper"); !errors.Is(err, retention.ErrInvalid) {
		t.Fatalf("keep empty episode err %v, want invalid", err)
	}
}
