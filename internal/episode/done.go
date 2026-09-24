package episode

import (
	"context"
	"errors"
	"fmt"

	"github.com/nrynss/keel/job"
)

// ErrStart reports a render job the service could not start after moving
// the episode. The service fails the episode, so no request leaves it in
// rendering with nothing to start its render.
var ErrStart = errors.New("episode: render job did not start")

// RequestRender moves a draft episode to rendering and starts its render
// job. The guarded move runs first, so two taps start one render and the
// second reports ErrIllegalTransition. Nothing else moves an episode here:
// the render pass advances it from there.
//
// A render kind at its limit refuses the start with job.ErrLimit. With
// WaitWhenBusy set, the episode then waits in rendering with no job, and
// RequestRender returns an empty job id with no error. The process starts
// that render once a slot frees. Any other refused start fails a still
// rendering episode, so it never sits in rendering with nothing to start
// it. An episode a racing render already moved on keeps its state.
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
	if err != nil && s.waitWhenBusy && errors.Is(err, job.ErrLimit) {
		return "", nil
	}
	if err != nil {
		start := errors.Join(err, fmt.Errorf("episode: request render %q: %w", episodeID, ErrStart))
		failErr := Transition(ctx, s.db, episodeID, StateRendering, StateFailed)
		if failErr != nil && !errors.Is(failErr, ErrIllegalTransition) {
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
