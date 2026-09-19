// The guest retention sweep.
//
// A sweep lists guests idle past the window, reading last seen time fresh
// at run time, and deletes each one in turn: every episode through the
// episode erasure, then stray media, then the session and user rows. The
// job snapshot records the guest list with per-episode erasure ids, so a
// resumed run reuses stuck erasures instead of starting over. Every delete
// repeats safely, so two runs converging on one guest still finish.

package retention

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/nrynss/keel/job"
	"github.com/nrynss/keel/mediastore"
	"github.com/nrynss/reprise/internal/privacy"
)

// sweptEpisode is one guest episode the sweep must erase, with the inner
// erasure jobs it already started. Done marks an episode whose erasure
// reads complete.
type sweptEpisode struct {
	// Episode is the episode id.
	Episode string `json:"episode"`
	// Erasures holds inner erasure job ids, oldest first.
	Erasures []string `json:"erasures,omitempty"`
	// Done marks an episode with nothing left owed.
	Done bool `json:"done,omitempty"`
}

// sweptGuest is one expired guest with the episodes the sweep must erase.
// RowsDone marks a guest whose session and user rows are gone.
type sweptGuest struct {
	// Guest is the guest user id.
	Guest string `json:"guest"`
	// Episodes lists every episode the guest owned at inventory time.
	Episodes []sweptEpisode `json:"episodes"`
	// RowsDone marks a guest with no session or user row left.
	RowsDone bool `json:"rows_done,omitempty"`
}

// sweepSnapshot is the ledger one sweep progress report carries. A run
// that resumes reads it from the interrupted record, so only unfinished
// guests erase again and stuck erasures retry from their recorded ids.
type sweepSnapshot struct {
	// Version guards the encoding. Only version one exists.
	Version int `json:"version"`
	// Cutoff is the unix time idle guests predate. Guests with a last
	// seen stamp below it expire.
	Cutoff int64 `json:"cutoff"`
	// Guests lists every guest this sweep owes work for.
	Guests []sweptGuest `json:"guests"`
}

// Sweep starts the retention sweep and returns the job id at once. The
// caller follows the job to done. The sweep reads expiry fresh inside the
// job, so guests who idled past the window since the last run expire now.
func (s *Service) Sweep(ctx context.Context) (string, error) {
	if s.runner == nil {
		return "", fmt.Errorf("retention: sweep: %w: no runner bound", ErrInvalid)
	}
	id, err := s.runner.StartKind(ctx, SweepName, func(ctx context.Context, progress func(job.Progress)) ([]byte, error) {
		return s.run(ctx, progress, sweepSnapshot{})
	})
	if err != nil {
		return "", fmt.Errorf("retention: sweep: %w", err)
	}
	return id, nil
}

// ReSweep restarts a failed or interrupted sweep from its recorded
// snapshot and returns the new job id at once. Stuck episode erasures
// retry from their recorded ids, because the episode rows are already
// gone and no fresh inventory could rebuild the provider list. A sweep
// with nothing recorded restarts from a fresh inventory, so a retry
// after a provider outage finishes the same guests.
func (s *Service) ReSweep(ctx context.Context, jobID string) (string, error) {
	if s.runner == nil {
		return "", fmt.Errorf("retention: re-sweep: %w: no runner bound", ErrInvalid)
	}
	if jobID == "" {
		return "", fmt.Errorf("retention: re-sweep: %w: empty job", ErrInvalid)
	}
	prior, err := s.snapshotOf(ctx, jobID)
	if err != nil {
		return "", err
	}
	id, err := s.runner.StartKind(ctx, SweepName, func(ctx context.Context, progress func(job.Progress)) ([]byte, error) {
		return s.run(ctx, progress, prior)
	})
	if err != nil {
		return "", fmt.Errorf("retention: re-sweep: %w", err)
	}
	return id, nil
}

// snapshotOf reads the latest recorded snapshot of one sweep job. A job
// with no snapshot yet restarts from a fresh inventory.
func (s *Service) snapshotOf(ctx context.Context, jobID string) (sweepSnapshot, error) {
	attempts, err := s.runner.Attempts(ctx, jobID)
	if err != nil {
		return sweepSnapshot{}, fmt.Errorf("retention: re-sweep %s: %w", jobID, err)
	}
	if len(attempts) == 0 {
		return sweepSnapshot{}, fmt.Errorf("retention: re-sweep %s: %w: no attempts recorded", jobID, ErrInvalid)
	}
	if attempts[len(attempts)-1].Kind != SweepName {
		return sweepSnapshot{}, fmt.Errorf("retention: re-sweep %s: %w: job is a %s job",
			jobID, ErrInvalid, attempts[len(attempts)-1].Kind)
	}
	prior := sweepSnapshot{}
	for i := len(attempts) - 1; i >= 0; i-- {
		if len(attempts[i].Progress.Detail) == 0 {
			continue
		}
		if err := json.Unmarshal(attempts[i].Progress.Detail, &prior); err != nil {
			return sweepSnapshot{}, fmt.Errorf("retention: re-sweep %s: %w", jobID, err)
		}
		if prior.Version != snapshotVersion {
			return sweepSnapshot{}, fmt.Errorf("retention: re-sweep %s: %w: unknown snapshot", jobID, ErrInvalid)
		}
		break
	}
	return prior, nil
}

// resume rebuilds the work of a sweep a restart left unfinished. It is the
// job kind resume hook. A record with no snapshot restarts from a fresh
// inventory, because nothing recorded yet names any guest.
func (s *Service) resume(rec job.Record) (job.Func, error) {
	snap := sweepSnapshot{}
	if len(rec.Progress.Detail) > 0 {
		if err := json.Unmarshal(rec.Progress.Detail, &snap); err != nil {
			return nil, fmt.Errorf("retention: resume %s: %w", rec.ID, err)
		}
		if snap.Version != snapshotVersion {
			return nil, fmt.Errorf("retention: resume %s: %w: unknown snapshot", rec.ID, ErrInvalid)
		}
	}
	return func(ctx context.Context, progress func(job.Progress)) ([]byte, error) {
		return s.run(ctx, progress, snap)
	}, nil
}

// run drives one sweep attempt. Prior names the guests an interrupted run
// already recorded. The run unions those with guests freshly expired, so a
// guest who idled past the window while the sweep was down still expires.
func (s *Service) run(ctx context.Context, progress func(job.Progress), prior sweepSnapshot) ([]byte, error) {
	runner := s.runner
	if runner == nil {
		return nil, fmt.Errorf("retention: sweep: %w: no runner bound", ErrInvalid)
	}
	cutoff := prior.Cutoff
	if cutoff == 0 {
		cutoff = s.now().Add(-s.window).Unix()
	}
	guests, err := s.plan(ctx, cutoff, prior.Guests)
	if err != nil {
		return nil, err
	}
	s.trust.set(guestIDs(guests))
	snap := sweepSnapshot{Version: snapshotVersion, Cutoff: cutoff, Guests: guests}
	s.publish(progress, snap)
	for i := range snap.Guests {
		if snap.Guests[i].RowsDone {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("retention: sweep: %w", err)
		}
		report := func() { s.publish(progress, snap) }
		if err := s.sweepGuest(ctx, runner, &snap.Guests[i], report); err != nil {
			return nil, err
		}
		s.publish(progress, snap)
	}
	return nil, nil
}

// plan unions unconfirmed prior guests with freshly expired ones. Fresh
// guests arrive with a live episode inventory, because their rows still
// name every episode. Confirmed guests stay out, because their rows are
// already gone.
func (s *Service) plan(ctx context.Context, cutoff int64, prior []sweptGuest) ([]sweptGuest, error) {
	kept := map[string]bool{}
	var guests []sweptGuest
	for _, g := range prior {
		if g.RowsDone {
			continue
		}
		kept[g.Guest] = true
		guests = append(guests, g)
	}
	expired, err := s.expired(ctx, cutoff)
	if err != nil {
		return nil, err
	}
	for _, id := range expired {
		if kept[id] {
			continue
		}
		episodes, err := s.episodes(ctx, id)
		if err != nil {
			return nil, err
		}
		list := make([]sweptEpisode, 0, len(episodes))
		for _, episode := range episodes {
			list = append(list, sweptEpisode{Episode: episode})
		}
		guests = append(guests, sweptGuest{Guest: id, Episodes: list})
	}
	return guests, nil
}

// expired lists guest users idle since before cutoff, oldest first. It
// reads last seen time at sweep time, never from a stamp taken earlier.
func (s *Service) expired(ctx context.Context, cutoff int64) ([]string, error) {
	rows, err := s.db.Reader().QueryContext(ctx,
		"SELECT id FROM users WHERE kind = 'guest' AND last_seen_at < ? ORDER BY id", cutoff)
	if err != nil {
		return nil, fmt.Errorf("retention: list expired: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("retention: list expired: %w", err)
		}
		out = append(out, id)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("retention: list expired: %w", err)
	}
	return out, nil
}

// episodes lists the episode ids one guest owns, in id order.
func (s *Service) episodes(ctx context.Context, guest string) ([]string, error) {
	rows, err := s.db.Reader().QueryContext(ctx,
		"SELECT id FROM episodes WHERE owner_id = ? ORDER BY id", guest)
	if err != nil {
		return nil, fmt.Errorf("retention: list episodes: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("retention: list episodes: %w", err)
		}
		out = append(out, id)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("retention: list episodes: %w", err)
	}
	return out, nil
}

// sweepGuest erases every episode of one guest, then removes the stray
// media and the session and user rows. It marks the guest done only after
// every delete confirms.
func (s *Service) sweepGuest(ctx context.Context, runner *job.Runner, guest *sweptGuest, report func()) error {
	for i := range guest.Episodes {
		if err := s.eraseEpisode(ctx, runner, guest.Guest, &guest.Episodes[i], report); err != nil {
			return err
		}
	}
	if err := s.deleteStrays(ctx, guest.Guest); err != nil {
		return err
	}
	if err := s.deleteGuestRows(ctx, guest.Guest); err != nil {
		return err
	}
	guest.RowsDone = true
	return nil
}

// eraseEpisode erases one episode through the episode erasure and waits
// for it to read complete. A gone episode row reads as done, because a
// previous run already erased it. A stuck erasure retries from its
// recorded id, because the rows are gone and no fresh inventory could
// rebuild the provider list.
func (s *Service) eraseEpisode(ctx context.Context, runner *job.Runner, guest string, episode *sweptEpisode, report func()) error {
	if episode.Done {
		return nil
	}
	if !s.episodePresent(ctx, episode.Episode) {
		return s.finishRecorded(ctx, runner, episode, report)
	}
	for round := 1; round <= episodeRetryRounds; round++ {
		id, err := s.startErasure(ctx, runner, guest, episode)
		if err != nil {
			return err
		}
		episode.Erasures = append(episode.Erasures, id)
		report()
		if err := s.waitErasure(ctx, runner, id); err != nil {
			return err
		}
		rep, err := s.inner.Eraser().Inspect(ctx, runner, id)
		if err != nil {
			return fmt.Errorf("retention: inspect erasure for %s: %w", episode.Episode, err)
		}
		if rep.Complete() {
			episode.Done = true
			report()
			return nil
		}
	}
	return fmt.Errorf("retention: erase %s: %w: episode still owed", episode.Episode, ErrIncomplete)
}

// finishRecorded finishes an episode whose row is already gone. Erasures
// recorded as complete read as done. Any other recorded erasure restarts
// from its id and waits, because only the recorded ref still names the
// provider copies. An episode with no recorded erasure went through
// another flow, so it reads as done.
func (s *Service) finishRecorded(ctx context.Context, runner *job.Runner, episode *sweptEpisode, report func()) error {
	if len(episode.Erasures) == 0 {
		episode.Done = true
		return nil
	}
	for _, id := range episode.Erasures {
		rep, err := s.inner.Eraser().Inspect(ctx, runner, id)
		if err != nil {
			return fmt.Errorf("retention: inspect erasure for %s: %w", episode.Episode, err)
		}
		if !rep.Complete() {
			return s.retryRecorded(ctx, runner, episode, id, report)
		}
	}
	episode.Done = true
	report()
	return nil
}

// retryRecorded restarts one stuck erasure from its recorded id and waits
// for it to read complete.
func (s *Service) retryRecorded(ctx context.Context, runner *job.Runner, episode *sweptEpisode, id string, report func()) error {
	for round := 1; round <= episodeRetryRounds; round++ {
		next, err := s.inner.ReErase(ctx, id)
		if errors.Is(err, privacy.ErrComplete) {
			episode.Done = true
			report()
			return nil
		}
		if err != nil {
			return fmt.Errorf("retention: re-erase %s: %w", episode.Episode, err)
		}
		episode.Erasures = append(episode.Erasures, next)
		report()
		if err := s.waitErasure(ctx, runner, next); err != nil {
			return err
		}
		rep, err := s.inner.Eraser().Inspect(ctx, runner, next)
		if err != nil {
			return fmt.Errorf("retention: inspect erasure for %s: %w", episode.Episode, err)
		}
		if rep.Complete() {
			episode.Done = true
			report()
			return nil
		}
		id = next
	}
	return fmt.Errorf("retention: erase %s: %w: episode still owed", episode.Episode, ErrIncomplete)
}

// startErasure starts a fresh episode erasure, or restarts the latest
// recorded one when the episode already owes provider deletes. Ownership
// checks against the recorded owner, because the sweep allowlisted every
// guest it runs for.
func (s *Service) startErasure(ctx context.Context, runner *job.Runner, guest string, episode *sweptEpisode) (string, error) {
	if n := len(episode.Erasures); n > 0 {
		id, err := s.inner.ReErase(ctx, episode.Erasures[n-1])
		if err == nil {
			return id, nil
		}
		rep, inspectErr := s.inner.Eraser().Inspect(ctx, runner, episode.Erasures[n-1])
		if inspectErr == nil && rep.Complete() {
			episode.Done = true
			return episode.Erasures[n-1], nil
		}
	}
	id, err := s.inner.Erase(ctx, episode.Episode)
	if err != nil {
		return "", fmt.Errorf("retention: erase %s for %s: %w", episode.Episode, guest, err)
	}
	return id, nil
}

// waitErasure polls one inner erasure until its job lands. Done means the
// erasure confirmed every target. Any other terminal means the provider
// still owes deletes, so the caller retries from the recorded id.
func (s *Service) waitErasure(ctx context.Context, runner *job.Runner, id string) error {
	deadline := time.Now().Add(60 * time.Second)
	for {
		attempts, err := runner.Attempts(ctx, id)
		if err != nil {
			return fmt.Errorf("retention: watch erasure: %w", err)
		}
		for _, rec := range attempts {
			switch rec.Status {
			case job.StatusDone:
				return nil
			case job.StatusError, job.StatusCancelled:
				return fmt.Errorf("retention: erasure %s ended %s", id, rec.Status)
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("retention: erasure %s never landed: %w", id, ErrIncomplete)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("retention: watch erasure: %w", ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// episodePresent reports whether the episode row still exists.
func (s *Service) episodePresent(ctx context.Context, episode string) bool {
	var total int
	err := s.db.Reader().QueryRowContext(ctx,
		"SELECT COUNT(*) FROM episodes WHERE id = ?", episode).Scan(&total)
	return err == nil && total > 0
}

// deleteStrays deletes every media blob the guest still owns. Episode
// erasures already removed grouped blobs, so survivors are ungrouped
// uploads. A missing blob reads as done, because deleting repeats safely.
func (s *Service) deleteStrays(ctx context.Context, guest string) error {
	rows, err := s.db.Reader().QueryContext(ctx, "SELECT id FROM media WHERE owner = ?", guest)
	if err != nil {
		return fmt.Errorf("retention: list stray media for %s: %w", guest, err)
	}
	defer func() { _ = rows.Close() }()
	var blobs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return fmt.Errorf("retention: list stray media for %s: %w", guest, err)
		}
		blobs = append(blobs, id)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("retention: list stray media for %s: %w", guest, err)
	}
	for _, blob := range blobs {
		if err := s.media.Delete(ctx, blob); err != nil {
			if errors.Is(err, mediastore.ErrNotFound) {
				continue
			}
			return fmt.Errorf("retention: delete stray %s: %w", blob, err)
		}
	}
	return nil
}

// deleteGuestRows removes the session rows and the user row of one guest.
// Foreign keys forbid the user row while episodes reference it, so this
// runs only after every episode erased. A missing user reads as done, and
// a user who stopped being a guest is skipped.
func (s *Service) deleteGuestRows(ctx context.Context, guest string) error {
	if _, err := s.db.Writer().ExecContext(ctx,
		"DELETE FROM guest_sessions WHERE user_id = ?", guest); err != nil {
		return fmt.Errorf("retention: delete sessions for %s: %w", guest, err)
	}
	var kind string
	err := s.db.Reader().QueryRowContext(ctx,
		"SELECT kind FROM users WHERE id = ?", guest).Scan(&kind)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("retention: read user %s: %w", guest, err)
	}
	if kind != "guest" {
		return nil
	}
	if _, err := s.db.Writer().ExecContext(ctx,
		"DELETE FROM users WHERE id = ? AND kind = 'guest'", guest); err != nil {
		return fmt.Errorf("retention: delete user %s: %w", guest, err)
	}
	return nil
}

// publish records one sweep ledger snapshot through the job progress
// channel, so the runner streams it and leaves it durable in the record.
func (s *Service) publish(progress func(job.Progress), snap sweepSnapshot) {
	detail, err := json.Marshal(snap)
	if err != nil {
		return
	}
	current := int64(doneGuests(snap.Guests))
	total := int64(len(snap.Guests))
	progress(job.Progress{Stage: "sweep", Current: &current, Total: &total, Detail: detail})
}

// guestIDs returns the guest ids in plan order.
func guestIDs(guests []sweptGuest) []string {
	out := make([]string, 0, len(guests))
	for _, g := range guests {
		out = append(out, g.Guest)
	}
	return out
}

// doneGuests counts guests with no rows left.
func doneGuests(guests []sweptGuest) int {
	total := 0
	for _, g := range guests {
		if g.RowsDone {
			total++
		}
	}
	return total
}
