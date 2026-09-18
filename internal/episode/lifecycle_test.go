package episode_test

import (
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"

	"github.com/nrynss/keel/sqlite"
	"github.com/nrynss/reprise/internal/episode"
	"github.com/nrynss/reprise/internal/store"
)

// states lists every lifecycle state the table test crosses.
var states = []episode.State{
	episode.StateRecording,
	episode.StateDraft,
	episode.StateRendering,
	episode.StateAnalysing,
	episode.StateReady,
	episode.StateFailed,
}

// workStates lists the states a retry may return to.
var workStates = []episode.State{
	episode.StateRecording,
	episode.StateDraft,
	episode.StateRendering,
	episode.StateAnalysing,
}

// openDatabase opens a migrated database file and closes it at cleanup.
// Records go to a discarding logger so a passing test stays quiet.
func openDatabase(t *testing.T) *sqlite.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "diary.db")
	db, err := sqlite.Open(t.Context(), sqlite.Config{
		Path:   path,
		Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() }) // the handle is discarded here, so a close failure cannot fail the test
	if _, err := store.Open(t.Context(), db); err != nil {
		t.Fatalf("migrate store: %v", err)
	}
	if _, err := db.Writer().ExecContext(t.Context(),
		"INSERT INTO users (id, kind, created_at, last_seen_at) VALUES ('owner-1', 'owner', 1, 2)"); err != nil {
		t.Fatalf("seed owner: %v", err)
	}
	return db
}

// beginEpisode starts one episode with a distinct number and fails the test
// on error.
func beginEpisode(t *testing.T, db *sqlite.DB, id string, number int64) {
	t.Helper()
	err := episode.Begin(t.Context(), db, episode.NewEpisode{
		ID:      id,
		OwnerID: "owner-1",
		Number:  number,
		Title:   "Episode " + id,
	})
	if err != nil {
		t.Fatalf("begin %s: %v", id, err)
	}
}

// plantEpisode starts one episode and forces its state to from with a direct
// update. Raw SQL only sets the scene here. The guarded move under test runs
// after.
func plantEpisode(t *testing.T, db *sqlite.DB, id string, number int64, from episode.State) {
	t.Helper()
	beginEpisode(t, db, id, number)
	if from == episode.StateRecording {
		return
	}
	if _, err := db.Writer().ExecContext(t.Context(),
		"UPDATE episodes SET state = ? WHERE id = ?", string(from), id); err != nil {
		t.Fatalf("plant %s in %s: %v", id, from, err)
	}
}

// legalMove reports whether the lifecycle allows from to reach to.
func legalMove(from, to episode.State) bool {
	if to == episode.StateFailed {
		return true
	}
	if from == episode.StateFailed {
		for _, s := range workStates {
			if to == s {
				return true
			}
		}
		return false
	}
	switch from {
	case episode.StateRecording:
		return to == episode.StateDraft
	case episode.StateDraft:
		return to == episode.StateRendering
	case episode.StateRendering:
		return to == episode.StateAnalysing
	case episode.StateAnalysing:
		return to == episode.StateReady
	default:
		return false
	}
}

// stateOf reads the current state and fails the test on error.
func stateOf(t *testing.T, db *sqlite.DB, id string) episode.State {
	t.Helper()
	got, err := episode.Current(t.Context(), db, id)
	if err != nil {
		t.Fatalf("current %s: %v", id, err)
	}
	return got
}

// TestBeginStartsRecordingPrivate checks a new episode starts in recording
// with private visibility, and a second begin on the id reports ErrExists.
func TestBeginStartsRecordingPrivate(t *testing.T) {
	t.Parallel()
	db := openDatabase(t)
	beginEpisode(t, db, "ep-1", 1)
	if got := stateOf(t, db, "ep-1"); got != episode.StateRecording {
		t.Fatalf("state = %s, want recording", got)
	}
	var visibility string
	err := db.Reader().QueryRowContext(t.Context(),
		"SELECT visibility FROM episodes WHERE id = 'ep-1'").Scan(&visibility)
	if err != nil {
		t.Fatalf("read visibility: %v", err)
	}
	if visibility != "private" {
		t.Fatalf("visibility = %q, want private", visibility)
	}
	err = episode.Begin(t.Context(), db, episode.NewEpisode{ID: "ep-1", OwnerID: "owner-1", Number: 2})
	if !errors.Is(err, episode.ErrExists) {
		t.Fatalf("second begin error = %v, want ErrExists", err)
	}
}

// TestTransitionTable tries every pair of states and allows only the listed
// moves. A refused move must leave the stored state untouched.
func TestTransitionTable(t *testing.T) {
	t.Parallel()
	db := openDatabase(t)
	var number int64
	for _, from := range states {
		for _, to := range states {
			number++
			id := fmt.Sprintf("ep-%s-%s", from, to)
			plantEpisode(t, db, id, number, from)
			err := episode.Transition(t.Context(), db, id, from, to)
			if legalMove(from, to) {
				if err != nil {
					t.Errorf("move %s to %s error = %v, want success", from, to, err)
				}
				if got := stateOf(t, db, id); got != to {
					t.Errorf("move %s to %s stored %s", from, to, got)
				}
				continue
			}
			if !errors.Is(err, episode.ErrIllegalTransition) {
				t.Errorf("move %s to %s error = %v, want ErrIllegalTransition", from, to, err)
			}
			if got := stateOf(t, db, id); got != from {
				t.Errorf("refused move %s to %s stored %s", from, to, got)
			}
		}
	}
}

// TestNamedMovesWalksHappyPath moves one episode through every named helper
// from recording to ready.
func TestNamedMovesWalksHappyPath(t *testing.T) {
	t.Parallel()
	db := openDatabase(t)
	beginEpisode(t, db, "ep-1", 1)
	steps := []struct {
		name string
		move func() error
		want episode.State
	}{
		{"stems uploaded", func() error { return episode.MarkStemsUploaded(t.Context(), db, "ep-1") }, episode.StateDraft},
		{"mark done", func() error { return episode.MarkDone(t.Context(), db, "ep-1") }, episode.StateRendering},
		{"rendered", func() error { return episode.MarkRendered(t.Context(), db, "ep-1") }, episode.StateAnalysing},
		{"ready", func() error { return episode.MarkReady(t.Context(), db, "ep-1") }, episode.StateReady},
	}
	for _, step := range steps {
		if err := step.move(); err != nil {
			t.Fatalf("%s: %v", step.name, err)
		}
		if got := stateOf(t, db, "ep-1"); got != step.want {
			t.Fatalf("after %s state = %s, want %s", step.name, got, step.want)
		}
	}
}

// TestRepeatMarkDoneStaysHarmless checks a second mark done reports
// ErrIllegalTransition and starts no second render.
func TestRepeatMarkDoneStaysHarmless(t *testing.T) {
	t.Parallel()
	db := openDatabase(t)
	plantEpisode(t, db, "ep-1", 1, episode.StateDraft)
	if err := episode.MarkDone(t.Context(), db, "ep-1"); err != nil {
		t.Fatalf("first mark done: %v", err)
	}
	if err := episode.MarkDone(t.Context(), db, "ep-1"); !errors.Is(err, episode.ErrIllegalTransition) {
		t.Fatalf("second mark done error = %v, want ErrIllegalTransition", err)
	}
	if got := stateOf(t, db, "ep-1"); got != episode.StateRendering {
		t.Fatalf("state = %s, want rendering", got)
	}
}

// TestConcurrentMarkDoneStartsOneRender runs two mark done requests at once
// and requires exactly one winner, so two taps start one render.
func TestConcurrentMarkDoneStartsOneRender(t *testing.T) {
	t.Parallel()
	db := openDatabase(t)
	plantEpisode(t, db, "ep-1", 1, episode.StateDraft)
	var group sync.WaitGroup
	results := make(chan error, 2)
	start := make(chan struct{})
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			results <- episode.MarkDone(t.Context(), db, "ep-1")
		}()
	}
	close(start)
	group.Wait()
	close(results)
	var won, refused int
	for err := range results {
		if err == nil {
			won++
			continue
		}
		if errors.Is(err, episode.ErrIllegalTransition) {
			refused++
			continue
		}
		t.Fatalf("mark done error = %v, want success or ErrIllegalTransition", err)
	}
	if won != 1 || refused != 1 {
		t.Fatalf("won = %d refused = %d, want exactly one winner", won, refused)
	}
	if got := stateOf(t, db, "ep-1"); got != episode.StateRendering {
		t.Fatalf("state = %s, want rendering", got)
	}
}

// TestFailKeepsStems fails an episode with stored stems and requires the
// episode and every stem to survive.
func TestFailKeepsStems(t *testing.T) {
	t.Parallel()
	db := openDatabase(t)
	plantEpisode(t, db, "ep-1", 1, episode.StateRendering)
	if _, err := db.Writer().ExecContext(t.Context(),
		"INSERT INTO stems (id, owner_id, episode_id, media_id, role, sample_rate, start_offset_ms) VALUES ('stem-1', 'owner-1', 'ep-1', 'media-1', 'user', 48000, 0)"); err != nil {
		t.Fatalf("seed stem: %v", err)
	}
	if err := episode.Fail(t.Context(), db, "ep-1"); err != nil {
		t.Fatalf("fail: %v", err)
	}
	if got := stateOf(t, db, "ep-1"); got != episode.StateFailed {
		t.Fatalf("state = %s, want failed", got)
	}
	var stems int
	if err := db.Reader().QueryRowContext(t.Context(),
		"SELECT COUNT(*) FROM stems WHERE episode_id = 'ep-1'").Scan(&stems); err != nil {
		t.Fatalf("count stems: %v", err)
	}
	if stems != 1 {
		t.Fatalf("stems = %d, want the stored stem to survive", stems)
	}
}

// TestRetryReturnsToFailedStep fails an episode mid render, retries the step
// and finishes the walk to ready. Retries to ready or to failed stay refused.
func TestRetryReturnsToFailedStep(t *testing.T) {
	t.Parallel()
	db := openDatabase(t)
	plantEpisode(t, db, "ep-1", 1, episode.StateRendering)
	if err := episode.Fail(t.Context(), db, "ep-1"); err != nil {
		t.Fatalf("fail: %v", err)
	}
	if err := episode.Retry(t.Context(), db, "ep-1", episode.StateRendering); err != nil {
		t.Fatalf("retry to rendering: %v", err)
	}
	if err := episode.MarkRendered(t.Context(), db, "ep-1"); err != nil {
		t.Fatalf("rendered after retry: %v", err)
	}
	if err := episode.MarkReady(t.Context(), db, "ep-1"); err != nil {
		t.Fatalf("ready after retry: %v", err)
	}
	plantEpisode(t, db, "ep-2", 2, episode.StateDraft)
	if err := episode.Fail(t.Context(), db, "ep-2"); err != nil {
		t.Fatalf("fail ep-2: %v", err)
	}
	for _, step := range []episode.State{episode.StateReady, episode.StateFailed, "broadcast"} {
		if err := episode.Retry(t.Context(), db, "ep-2", step); !errors.Is(err, episode.ErrIllegalTransition) {
			t.Fatalf("retry to %q error = %v, want ErrIllegalTransition", step, err)
		}
	}
	if err := episode.Retry(t.Context(), db, "ep-1", episode.StateDraft); !errors.Is(err, episode.ErrIllegalTransition) {
		t.Fatalf("retry from ready error = %v, want ErrIllegalTransition", err)
	}
}

// TestMarkInterruptedFailsLiveWork moves every working state to failed,
// leaves ready alone and treats a late interrupt on failed as harmless.
func TestMarkInterruptedFailsLiveWork(t *testing.T) {
	t.Parallel()
	db := openDatabase(t)
	var number int64
	for _, from := range workStates {
		number++
		id := fmt.Sprintf("ep-live-%s", from)
		plantEpisode(t, db, id, number, from)
		if err := episode.MarkInterrupted(t.Context(), db, id); err != nil {
			t.Errorf("interrupt in %s: %v", from, err)
		}
		if got := stateOf(t, db, id); got != episode.StateFailed {
			t.Errorf("after interrupt in %s state = %s, want failed", from, got)
		}
	}
	plantEpisode(t, db, "ep-ready", 100, episode.StateReady)
	if err := episode.MarkInterrupted(t.Context(), db, "ep-ready"); !errors.Is(err, episode.ErrIllegalTransition) {
		t.Fatalf("interrupt ready error = %v, want ErrIllegalTransition", err)
	}
	plantEpisode(t, db, "ep-failed", 101, episode.StateFailed)
	if err := episode.MarkInterrupted(t.Context(), db, "ep-failed"); err != nil {
		t.Fatalf("late interrupt on failed: %v", err)
	}
	if err := episode.MarkInterrupted(t.Context(), db, "ep-missing"); !errors.Is(err, episode.ErrNotFound) {
		t.Fatalf("interrupt missing error = %v, want ErrNotFound", err)
	}
}

// TestUnknownIdsAndArguments checks every entry rejects nil databases, empty
// ids and unknown states, and reports missing rows as not found.
func TestUnknownIdsAndArguments(t *testing.T) {
	t.Parallel()
	db := openDatabase(t)
	beginEpisode(t, db, "ep-1", 1)
	if err := episode.Begin(t.Context(), nil, episode.NewEpisode{ID: "x", OwnerID: "owner-1"}); !errors.Is(err, episode.ErrInvalid) {
		t.Errorf("begin nil db error = %v, want ErrInvalid", err)
	}
	if err := episode.Begin(t.Context(), db, episode.NewEpisode{}); !errors.Is(err, episode.ErrInvalid) {
		t.Errorf("begin empty error = %v, want ErrInvalid", err)
	}
	if _, err := episode.Current(t.Context(), nil, "ep-1"); !errors.Is(err, episode.ErrInvalid) {
		t.Errorf("current nil db error = %v, want ErrInvalid", err)
	}
	if _, err := episode.Current(t.Context(), db, "ep-missing"); !errors.Is(err, episode.ErrNotFound) {
		t.Errorf("current missing error = %v, want ErrNotFound", err)
	}
	if err := episode.Transition(t.Context(), db, "ep-1", episode.StateRecording, "broadcast"); !errors.Is(err, episode.ErrInvalid) {
		t.Errorf("transition unknown state error = %v, want ErrInvalid", err)
	}
	if err := episode.Transition(t.Context(), db, "ep-missing", episode.StateRecording, episode.StateDraft); !errors.Is(err, episode.ErrNotFound) {
		t.Errorf("transition missing error = %v, want ErrNotFound", err)
	}
	if err := episode.MarkDone(t.Context(), db, "ep-missing"); !errors.Is(err, episode.ErrNotFound) {
		t.Errorf("mark done missing error = %v, want ErrNotFound", err)
	}
	if err := episode.Fail(t.Context(), db, "ep-missing"); !errors.Is(err, episode.ErrNotFound) {
		t.Errorf("fail missing error = %v, want ErrNotFound", err)
	}
	if err := episode.Retry(t.Context(), nil, "ep-1", episode.StateDraft); !errors.Is(err, episode.ErrInvalid) {
		t.Errorf("retry nil db error = %v, want ErrInvalid", err)
	}
}
