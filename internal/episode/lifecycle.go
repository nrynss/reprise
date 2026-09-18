// Package episode guards the episode lifecycle.
//
// An episode moves through recording, draft, rendering, analysing, ready and
// failed. Visibility stays outside state and always starts private. Every move
// is one SQL update guarded on the current state, so a repeated request never
// applies twice. Creation inserts the row in recording, because a session
// start is the only entry.
package episode

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/nrynss/keel/id"
	"github.com/nrynss/keel/sqlite"
)

// ErrInvalid reports a call the package cannot honour, such as a nil database,
// an empty id or an unknown state.
var ErrInvalid = errors.New("episode: invalid argument")

// ErrExists reports a begin for an episode id the database already holds.
var ErrExists = errors.New("episode: episode already exists")

// ErrNotFound reports a move for an episode id the database holds nothing for.
var ErrNotFound = errors.New("episode: episode not found")

// ErrIllegalTransition reports a move the lifecycle forbids, or a move
// requested from a state the episode is not in.
var ErrIllegalTransition = errors.New("episode: illegal transition")

// State names one episode lifecycle state.
type State string

// StateRecording marks an episode with a live or newly ended session whose
// stems have not both uploaded.
const StateRecording State = "recording"

// StateDraft marks an episode with both stems stored and no render started.
const StateDraft State = "draft"

// StateRendering marks an episode whose render job is running.
const StateRendering State = "rendering"

// StateAnalysing marks an episode whose render finished and whose analysis and
// memory jobs are running.
const StateAnalysing State = "analysing"

// StateReady marks a finished episode that the analysis and memory jobs
// completed.
const StateReady State = "ready"

// StateFailed marks an episode whose job failed or stopped on restart, with
// every stem kept.
const StateFailed State = "failed"

// allowed names every legal move. A missing pair is forbidden.
var allowed = map[State]map[State]bool{
	StateRecording: {StateDraft: true, StateFailed: true},
	StateDraft:     {StateRendering: true, StateFailed: true},
	StateRendering: {StateAnalysing: true, StateFailed: true},
	StateAnalysing: {StateReady: true, StateFailed: true},
	StateReady:     {StateFailed: true},
	StateFailed: {
		StateRecording: true,
		StateDraft:     true,
		StateRendering: true,
		StateAnalysing: true,
		StateFailed:    true,
	},
}

// NewEpisode carries the row a new episode needs. Begin fills the rest.
type NewEpisode struct {
	// ID identifies the episode. It must be unique.
	ID string
	// OwnerID identifies the owning user row.
	OwnerID string
	// Number orders the episode within its owner.
	Number int64
	// Title names the episode. It may be empty.
	Title string
}

// known reports whether s names a lifecycle state.
func known(s State) bool {
	switch s {
	case StateRecording, StateDraft, StateRendering, StateAnalysing, StateReady, StateFailed:
		return true
	default:
		return false
	}
}

// Begin inserts ep as a private episode in recording. It returns ErrExists
// when the id already exists. Session start is the only caller, because
// recording is the only entry to the lifecycle.
func Begin(ctx context.Context, db *sqlite.DB, ep NewEpisode) error {
	if db == nil || ep.ID == "" || ep.OwnerID == "" {
		return fmt.Errorf("episode: begin %q: %w", ep.ID, ErrInvalid)
	}
	var one int
	err := db.Reader().QueryRowContext(ctx, "SELECT 1 FROM episodes WHERE id = ?", ep.ID).Scan(&one)
	if err == nil {
		return fmt.Errorf("episode: begin %q: %w", ep.ID, ErrExists)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("episode: begin %q: %w", ep.ID, err)
	}
	token, err := id.New()
	if err != nil {
		return fmt.Errorf("episode: begin %q: %w", ep.ID, err)
	}
	_, err = db.Writer().ExecContext(ctx,
		"INSERT INTO episodes (id, owner_id, number, title, state, visibility, share_token, seeded) VALUES (?, ?, ?, ?, ?, ?, ?, 0)",
		ep.ID, ep.OwnerID, ep.Number, ep.Title, string(StateRecording), "private", token)
	if err != nil {
		return fmt.Errorf("episode: begin %q: %w", ep.ID, err)
	}
	return nil
}

// Current returns the state of id. It returns ErrNotFound for an unknown id.
func Current(ctx context.Context, db *sqlite.DB, id string) (State, error) {
	if db == nil || id == "" {
		return "", fmt.Errorf("episode: current %q: %w", id, ErrInvalid)
	}
	var state string
	err := db.Reader().QueryRowContext(ctx, "SELECT state FROM episodes WHERE id = ?", id).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("episode: current %q: %w", id, ErrNotFound)
	}
	if err != nil {
		return "", fmt.Errorf("episode: current %q: %w", id, err)
	}
	return State(state), nil
}

// Transition moves id from want to next in one guarded update. It succeeds
// only when the pair is legal and the row still holds want, so two racing
// callers produce one winner. It returns ErrIllegalTransition for a forbidden
// pair or a stale want, and ErrNotFound for an unknown id.
func Transition(ctx context.Context, db *sqlite.DB, id string, want, next State) error {
	if db == nil || id == "" || !known(want) || !known(next) {
		return fmt.Errorf("episode: transition %q: %w", id, ErrInvalid)
	}
	if !allowed[want][next] {
		return fmt.Errorf("episode: transition %q from %s to %s: %w", id, want, next, ErrIllegalTransition)
	}
	res, err := db.Writer().ExecContext(ctx,
		"UPDATE episodes SET state = ? WHERE id = ? AND state = ?", string(next), id, string(want))
	if err != nil {
		return fmt.Errorf("episode: transition %q: %w", id, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("episode: transition %q: %w", id, err)
	}
	if affected == 1 {
		return nil
	}
	cur, err := Current(ctx, db, id)
	if err != nil {
		return err
	}
	return fmt.Errorf("episode: transition %q from %s: episode is %s: %w", id, want, cur, ErrIllegalTransition)
}

// MarkStemsUploaded moves a recording episode to draft when both stems finish
// uploading. The upload completion is the only caller.
func MarkStemsUploaded(ctx context.Context, db *sqlite.DB, id string) error {
	return Transition(ctx, db, id, StateRecording, StateDraft)
}

// MarkDone moves a draft episode to rendering. The user marking the episode
// done is the only trigger, so the expensive pass runs once per episode
// rather than once per edit.
func MarkDone(ctx context.Context, db *sqlite.DB, id string) error {
	return Transition(ctx, db, id, StateDraft, StateRendering)
}

// MarkRendered moves a rendering episode to analysing when the render job
// finishes.
func MarkRendered(ctx context.Context, db *sqlite.DB, id string) error {
	return Transition(ctx, db, id, StateRendering, StateAnalysing)
}

// MarkReady moves an analysing episode to ready when the analysis and memory
// jobs finish.
func MarkReady(ctx context.Context, db *sqlite.DB, id string) error {
	return Transition(ctx, db, id, StateAnalysing, StateReady)
}

// Fail moves an episode in any state to failed when a job ends failed or
// interrupted. Stems stay in place, so a retry resumes from the failed step.
// A repeat call on a failed episode succeeds without changing anything.
func Fail(ctx context.Context, db *sqlite.DB, id string) error {
	if db == nil || id == "" {
		return fmt.Errorf("episode: fail %q: %w", id, ErrInvalid)
	}
	res, err := db.Writer().ExecContext(ctx,
		"UPDATE episodes SET state = ? WHERE id = ?", string(StateFailed), id)
	if err != nil {
		return fmt.Errorf("episode: fail %q: %w", id, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("episode: fail %q: %w", id, err)
	}
	if affected == 1 {
		return nil
	}
	return fmt.Errorf("episode: fail %q: %w", id, ErrNotFound)
}

// Retry moves a failed episode back to the step that failed. The user retrying
// is the only caller. Ready is never a retry target, because an episode
// reaches ready only through analysis.
func Retry(ctx context.Context, db *sqlite.DB, id string, step State) error {
	if db == nil || id == "" {
		return fmt.Errorf("episode: retry %q: %w", id, ErrInvalid)
	}
	switch step {
	case StateRecording, StateDraft, StateRendering, StateAnalysing:
		return Transition(ctx, db, id, StateFailed, step)
	default:
		return fmt.Errorf("episode: retry %q to %s: %w", id, step, ErrIllegalTransition)
	}
}

// MarkInterrupted moves an episode with a live job to failed after a restart.
// A restart never reruns a paid job silently, so the episode waits for an
// explicit retry. Episodes with no live job stay put, and a late interrupt on
// an already failed episode succeeds without changing anything.
func MarkInterrupted(ctx context.Context, db *sqlite.DB, id string) error {
	if db == nil || id == "" {
		return fmt.Errorf("episode: mark interrupted %q: %w", id, ErrInvalid)
	}
	res, err := db.Writer().ExecContext(ctx,
		"UPDATE episodes SET state = ? WHERE id = ? AND state IN (?, ?, ?, ?)",
		string(StateFailed), id,
		string(StateRecording), string(StateDraft), string(StateRendering), string(StateAnalysing))
	if err != nil {
		return fmt.Errorf("episode: mark interrupted %q: %w", id, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("episode: mark interrupted %q: %w", id, err)
	}
	if affected == 1 {
		return nil
	}
	cur, err := Current(ctx, db, id)
	if err != nil {
		return err
	}
	if cur == StateFailed {
		return nil
	}
	return fmt.Errorf("episode: mark interrupted %q in %s: %w", id, cur, ErrIllegalTransition)
}
