package episode_test

import (
	"errors"
	"testing"

	"github.com/nrynss/keel/sqlite"
	"github.com/nrynss/reprise/internal/episode"
)

// stemCount counts the stem rows on one episode and fails the test on error.
func stemCount(t *testing.T, db *sqlite.DB, episodeID string) int {
	t.Helper()
	var count int
	if err := db.Reader().QueryRowContext(t.Context(),
		`SELECT COUNT(*) FROM stems WHERE episode_id = ?`, episodeID).Scan(&count); err != nil {
		t.Fatalf("count stems: %v", err)
	}
	return count
}

// stemMedia reads the stored blob id for one stem role and fails the test
// on error.
func stemMedia(t *testing.T, db *sqlite.DB, episodeID, role string) string {
	t.Helper()
	var mediaID string
	if err := db.Reader().QueryRowContext(t.Context(),
		`SELECT media_id FROM stems WHERE episode_id = ? AND role = ? ORDER BY rowid ASC LIMIT 1`,
		episodeID, role).Scan(&mediaID); err != nil {
		t.Fatalf("read %s stem: %v", role, err)
	}
	return mediaID
}

// completeFixture links two fixture blobs and fails the test on error. It
// returns whether the guarded move won here.
func completeFixture(t *testing.T, svc *episode.Service, ownerID, episodeID, userMedia, hostMedia string) bool {
	t.Helper()
	moved, err := svc.CompleteStems(t.Context(), ownerID, episodeID, userMedia, hostMedia, 48000, 24000)
	if err != nil {
		t.Fatalf("complete stems: %v", err)
	}
	return moved
}

// TestCompleteStemsMovesRecordingToDraftOnce links two fixture blobs twice
// and requires one move to draft, two stem rows, and no second move.
func TestCompleteStemsMovesRecordingToDraftOnce(t *testing.T) {
	t.Parallel()
	db := openDatabase(t)
	beginEpisode(t, db, "ep-1", 1)
	svc, err := episode.NewService(episode.Config{DB: db})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	if moved := completeFixture(t, svc, "owner-1", "ep-1", "blob-user", "blob-host"); !moved {
		t.Fatal("first completion moved = false, want the guarded win")
	}
	if got := stateOf(t, db, "ep-1"); got != episode.StateDraft {
		t.Fatalf("state = %s, want draft", got)
	}
	if got := stemCount(t, db, "ep-1"); got != 2 {
		t.Fatalf("stems = %d, want the user and host rows", got)
	}
	if moved := completeFixture(t, svc, "owner-1", "ep-1", "blob-user", "blob-host"); moved {
		t.Fatal("second completion moved = true, want no second move")
	}
	if got := stateOf(t, db, "ep-1"); got != episode.StateDraft {
		t.Fatalf("state = %s, want draft to survive the repeat", got)
	}
	if got := stemCount(t, db, "ep-1"); got != 2 {
		t.Fatalf("stems = %d, want no second pair", got)
	}
}

// TestCompleteStemsKeepsFirstPair repeats a completion with different blobs
// and requires the stored pair to stay the first one.
func TestCompleteStemsKeepsFirstPair(t *testing.T) {
	t.Parallel()
	db := openDatabase(t)
	beginEpisode(t, db, "ep-1", 1)
	svc, err := episode.NewService(episode.Config{DB: db})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	completeFixture(t, svc, "owner-1", "ep-1", "blob-a-user", "blob-a-host")
	if moved := completeFixture(t, svc, "owner-1", "ep-1", "blob-b-user", "blob-b-host"); moved {
		t.Fatal("repeat moved = true, want the first win to stand")
	}
	if got := stemMedia(t, db, "ep-1", "user"); got != "blob-a-user" {
		t.Fatalf("user stem = %q, want the first blob", got)
	}
	if got := stemMedia(t, db, "ep-1", "host"); got != "blob-a-host" {
		t.Fatalf("host stem = %q, want the first blob", got)
	}
}

// TestCompleteStemsHealsInterruptedLinking plants a draft episode with one
// stem row, as a crash between the move and the link leaves it, and requires
// a repeat to fill the missing row without moving.
func TestCompleteStemsHealsInterruptedLinking(t *testing.T) {
	t.Parallel()
	db := openDatabase(t)
	plantEpisode(t, db, "ep-1", 1, episode.StateDraft)
	if _, err := db.Writer().ExecContext(t.Context(),
		`INSERT INTO stems (id, owner_id, episode_id, media_id, role, sample_rate, start_offset_ms)
		 VALUES ('stem-1', 'owner-1', 'ep-1', 'blob-user', 'user', 48000, 0)`); err != nil {
		t.Fatalf("seed user stem: %v", err)
	}
	svc, err := episode.NewService(episode.Config{DB: db})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	if moved := completeFixture(t, svc, "owner-1", "ep-1", "blob-user", "blob-host"); moved {
		t.Fatal("healing moved = true, want the draft to stand")
	}
	if got := stemCount(t, db, "ep-1"); got != 2 {
		t.Fatalf("stems = %d, want the missing host row filled", got)
	}
	if got := stemMedia(t, db, "ep-1", "host"); got != "blob-host" {
		t.Fatalf("host stem = %q, want the retried blob", got)
	}
}

// TestCompleteStemsRefusals checks unknown and foreign episodes, episodes
// past draft, and malformed pairs each fail with the right sentinel.
func TestCompleteStemsRefusals(t *testing.T) {
	t.Parallel()
	db := openDatabase(t)
	seedOwner(t, db, "owner-2")
	beginEpisode(t, db, "ep-1", 1)
	plantEpisode(t, db, "ep-2", 2, episode.StateRendering)
	svc, err := episode.NewService(episode.Config{DB: db})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	ctx := t.Context()
	if _, err := svc.CompleteStems(ctx, "owner-1", "missing", "blob-u", "blob-h", 48000, 24000); !errors.Is(err, episode.ErrNotFound) {
		t.Fatalf("missing error = %v, want ErrNotFound", err)
	}
	if _, err := svc.CompleteStems(ctx, "owner-2", "ep-1", "blob-u", "blob-h", 48000, 24000); !errors.Is(err, episode.ErrNotFound) {
		t.Fatalf("foreign error = %v, want ErrNotFound", err)
	}
	if _, err := svc.CompleteStems(ctx, "owner-1", "ep-2", "blob-u", "blob-h", 48000, 24000); !errors.Is(err, episode.ErrIllegalTransition) {
		t.Fatalf("rendering error = %v, want ErrIllegalTransition", err)
	}
	for name, call := range map[string]func() error{
		"empty owner": func() error {
			_, err := svc.CompleteStems(ctx, "", "ep-1", "blob-u", "blob-h", 48000, 24000)
			return err
		},
		"empty episode": func() error {
			_, err := svc.CompleteStems(ctx, "owner-1", "", "blob-u", "blob-h", 48000, 24000)
			return err
		},
		"empty blob": func() error {
			_, err := svc.CompleteStems(ctx, "owner-1", "ep-1", "", "blob-h", 48000, 24000)
			return err
		},
		"same blob twice": func() error {
			_, err := svc.CompleteStems(ctx, "owner-1", "ep-1", "blob-same", "blob-same", 48000, 24000)
			return err
		},
		"zero rate": func() error {
			_, err := svc.CompleteStems(ctx, "owner-1", "ep-1", "blob-u", "blob-h", 0, 24000)
			return err
		},
	} {
		if err := call(); !errors.Is(err, episode.ErrInvalid) {
			t.Fatalf("%s error = %v, want ErrInvalid", name, err)
		}
	}
	if _, err := svc.CompleteStems(ctx, "owner-1", "ep-1", "blob-u", "blob-h", 48000, 24000); err != nil {
		t.Fatalf("valid completion after refusals: %v", err)
	}
}

// TestCompleteStemsNeedsService checks a nil service refuses before touching
// any row.
func TestCompleteStemsNeedsService(t *testing.T) {
	t.Parallel()
	var svc *episode.Service
	if _, err := svc.CompleteStems(t.Context(), "owner-1", "ep-1", "blob-u", "blob-h", 48000, 24000); !errors.Is(err, episode.ErrInvalid) {
		t.Fatalf("nil service error = %v, want ErrInvalid", err)
	}
}
