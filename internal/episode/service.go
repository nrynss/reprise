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
	"fmt"

	"github.com/nrynss/keel/job"
	"github.com/nrynss/keel/sqlite"
)

// Starter starts one named job kind and returns its id at once. The job
// runner implements it. The service declares the seam, so tests bind a
// fake without opening a runner.
type Starter interface {
	// StartKind runs fn as a job of the named kind.
	StartKind(ctx context.Context, kind string, fn job.Func) (string, error)
}

// Renderer builds the render work for one episode. The render resolver
// implements it. The service declares the seam, so the episode package
// never imports the render pass it triggers.
type Renderer interface {
	// Func returns the work that renders one episode.
	Func(ownerID, episodeID string) job.Func
}

// Service carries the owner scoped episode reads and writes the HTTP
// layer serves. The database is required. The starter, kind, and renderer
// are required only for the mark done path, so lists and reads run in a
// process with no job runner. The transcript kind is required only for
// the outcome read, so processes without a runner still serve the
// detail. A Service is safe for concurrent use.
type Service struct {
	db             *sqlite.DB
	starter        Starter
	renderKind     string
	render         Renderer
	transcriptKind string
	waitWhenBusy   bool
}

// Config carries what NewService needs. DB is the shared diary handle.
// Starter, RenderKind, and Render name the render job the mark done path
// starts. Leave those three unset when the process starts no renders.
type Config struct {
	// DB is the shared diary handle. It must not be nil.
	DB *sqlite.DB
	// Starter starts the render job. It must not be nil to mark done.
	Starter Starter
	// RenderKind is the job kind the render runs under.
	RenderKind string
	// Render builds the render work for one episode.
	Render Renderer
	// TranscriptKind names the transcript job kind the outcome read
	// matches. Leave it empty when the process starts no transcript
	// jobs, and the outcome read reports no pass.
	TranscriptKind string
	// WaitWhenBusy leaves a marked episode in rendering when the render
	// kind refuses a start with job.ErrLimit. Set it only when the process
	// later starts a render for every rendering episode with no render
	// job. Left unset, a refused start fails the episode.
	WaitWhenBusy bool
}

// NewService returns a Service over cfg. It reports ErrInvalid for a nil
// database, because every operation reads owned rows.
func NewService(cfg Config) (*Service, error) {
	if cfg.DB == nil {
		return nil, fmt.Errorf("episode: new service: %w: database must not be nil", ErrInvalid)
	}
	return &Service{
		db:             cfg.DB,
		starter:        cfg.Starter,
		renderKind:     cfg.RenderKind,
		render:         cfg.Render,
		transcriptKind: cfg.TranscriptKind,
		waitWhenBusy:   cfg.WaitWhenBusy,
	}, nil
}
