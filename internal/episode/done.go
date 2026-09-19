package episode

import (
	"context"
	"errors"
	"fmt"
)

// ErrStart reports a render job the service could not start after moving
// the episode. The episode waits in failed for an explicit retry, so no
// request leaves it in rendering with no job behind it.
var ErrStart = errors.New("episode: render job did not start")

// RequestRender moves a draft episode to rendering and starts its render
// job. The guarded move runs first, so two taps start one render and the
// second reports ErrIllegalTransition. Nothing else moves an episode here:
// the render pass advances it from there. A job that never starts fails
// the episode, so a retry resumes from the failed step instead of leaving
// the episode in rendering with no job behind it.
func (s *Service) RequestRender(ctx context.Context, ownerID, episodeID string) (string, error) {
	if s == nil || s.db == nil || s.starter == nil || s.renderKind == "" || s.render == nil {
		return "", fmt.Errorf("episode: request render %q: %w", episodeID, ErrInvalid)
	}
	if ownerID == "" || episodeID == "" {
		return "", fmt.Errorf("episode: request render %q: %w", episodeID, ErrInvalid)
	}
	if _, err := s.Get(ctx, ownerID, episodeID); err != nil {
		return "", err
	}
	if err := MarkDone(ctx, s.db, episodeID); err != nil {
		return "", err
	}
	jobID, err := s.starter.StartKind(ctx, s.renderKind, s.render.Func(ownerID, episodeID))
	if err != nil {
		start := errors.Join(err, fmt.Errorf("episode: request render %q: %w", episodeID, ErrStart))
		if failErr := Fail(ctx, s.db, episodeID); failErr != nil {
			return "", errors.Join(start, failErr)
		}
		return "", start
	}
	return jobID, nil
}
