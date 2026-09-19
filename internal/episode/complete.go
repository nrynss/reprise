package episode

import (
	"context"
	"errors"
	"fmt"

	"github.com/nrynss/keel/id"
	"github.com/nrynss/reprise/internal/transcript"
)

// CompleteStems records both stem blobs for an episode the owner holds and
// moves recording to draft through the guarded transition. The upload
// completion is the only caller. The first call wins. It stores the two stem
// rows and moves the episode, reporting moved true. A repeat call keeps the
// first pair, leaves the state alone, and reports moved false with no error,
// so a retried completion schedules nothing twice. A repeat also fills rows
// the winning call never wrote, so a crash between the move and the schedule
// heals on retry. A repeat on a draft or failed episode is harmless, so the
// answer below can report the standing pass outcome instead of refusing.
// Unknown and foreign episodes both report ErrNotFound. Any other state
// reports ErrIllegalTransition.
func (s *Service) CompleteStems(ctx context.Context, ownerID, episodeID, userMediaID, hostMediaID string, userSampleRate, hostSampleRate int64) (bool, error) {
	if s == nil || s.db == nil || ownerID == "" || episodeID == "" || userMediaID == "" || hostMediaID == "" {
		return false, fmt.Errorf("episode: complete stems %q: %w", episodeID, ErrInvalid)
	}
	if userMediaID == hostMediaID {
		return false, fmt.Errorf("episode: complete stems %q: %w: user and host stems differ", episodeID, ErrInvalid)
	}
	if userSampleRate <= 0 || hostSampleRate <= 0 {
		return false, fmt.Errorf("episode: complete stems %q: %w: sample rates stay positive", episodeID, ErrInvalid)
	}
	if _, err := s.Get(ctx, ownerID, episodeID); err != nil {
		return false, err
	}
	if err := s.linkStems(ctx, episodeID, ownerID, userMediaID, hostMediaID, userSampleRate, hostSampleRate); err != nil {
		return false, err
	}
	if err := MarkStemsUploaded(ctx, s.db, episodeID); err != nil {
		if errors.Is(err, ErrIllegalTransition) {
			cur, cerr := Current(ctx, s.db, episodeID)
			if cerr != nil {
				return false, cerr
			}
			if cur == StateDraft || cur == StateFailed {
				return false, nil
			}
		}
		return false, err
	}
	return true, nil
}

// linkStems stores the two stem rows for an episode and leaves rows a first
// completion already stored untouched. The pair index keeps one row per role,
// so a repeat with different blob ids keeps the first pair, because a
// transcript job may already read it. A clash inserts nothing and reports no
// error, so concurrent completions converge on one pair with no orphan rows.
func (s *Service) linkStems(ctx context.Context, episodeID, ownerID, userMediaID, hostMediaID string, userSampleRate, hostSampleRate int64) error {
	type want struct {
		role       string
		mediaID    string
		sampleRate int64
	}
	for _, w := range []want{
		{role: transcript.RoleUser, mediaID: userMediaID, sampleRate: userSampleRate},
		{role: transcript.RoleHost, mediaID: hostMediaID, sampleRate: hostSampleRate},
	} {
		stemID, err := id.New()
		if err != nil {
			return fmt.Errorf("episode: link stems %q: %w", episodeID, err)
		}
		if _, err := s.db.Writer().ExecContext(ctx,
			`INSERT INTO stems (id, owner_id, episode_id, media_id, role, sample_rate, start_offset_ms)
			 VALUES (?, ?, ?, ?, ?, ?, 0)
			 ON CONFLICT(episode_id, role) DO NOTHING`,
			stemID, ownerID, episodeID, w.mediaID, w.role, w.sampleRate); err != nil {
			return fmt.Errorf("episode: link stems %q: %w", episodeID, err)
		}
	}
	return nil
}
