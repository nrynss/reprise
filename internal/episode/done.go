package episode

import (
	"context"
	"errors"
	"fmt"

	"github.com/nrynss/keel/job"
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
	jobID, err := s.startRender(ctx, ownerID, episodeID)
	if err != nil {
		start := errors.Join(err, fmt.Errorf("episode: request render %q: %w", episodeID, ErrStart))
		if failErr := Fail(ctx, s.db, episodeID); failErr != nil {
			return "", errors.Join(start, failErr)
		}
		return "", start
	}
	return jobID, nil
}

// EpisodeStarter starts one named job kind for one episode and records the
// episode on the job before it returns. The binary scheduler implements
// it. When the configured starter implements it too, mark done starts the
// render through it, so the render job carries its episode from the first
// read on.
type EpisodeStarter interface {
	// StartEpisodeKind runs fn as a job of the named kind for one episode.
	StartEpisodeKind(ctx context.Context, kind, ownerID, episodeID string, fn job.Func) (string, error)
}

// startRender starts the render job, through the episode seam when the
// starter offers it.
func (s *Service) startRender(ctx context.Context, ownerID, episodeID string) (string, error) {
	fn := s.render.Func(ownerID, episodeID)
	if stamped, ok := s.starter.(EpisodeStarter); ok {
		return stamped.StartEpisodeKind(ctx, s.renderKind, ownerID, episodeID, fn)
	}
	return s.starter.StartKind(ctx, s.renderKind, fn)
}
