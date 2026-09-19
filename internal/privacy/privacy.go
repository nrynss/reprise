// Package privacy carries publishing and erasure for diary episodes.
//
// Every episode starts private. Publishing mints an unguessable share
// token and marks the finished render public, so a signed-out visitor
// behind the token hears the render and sees the cover and nothing else.
// Stems, transcripts and threads stay private always, because the share
// handler serves no route to them. Unpublishing revokes the token and
// returns the media to private. Erasing fans out over every place a
// recording landed through the shared erasure library, which retries
// every target until it confirms and resumes after a restart.
package privacy

import (
	"context"
	"errors"
	"fmt"

	"github.com/nrynss/keel/erase"
	"github.com/nrynss/keel/job"
	"github.com/nrynss/keel/sqlite"
	"github.com/nrynss/reprise/internal/assemblyai"
)

// Visibility names the values the episodes visibility column holds.
// Private is the default every new episode carries. Public marks an
// episode its owner shared through a token.
const (
	// VisibilityPrivate keeps the episode and its media owner only.
	VisibilityPrivate = "private"
	// VisibilityPublic opens the render and the cover behind the token.
	VisibilityPublic = "public"
)

// Sentinel errors. Every failure path this package produces wraps one of
// these, so callers branch with errors.Is.
var (
	// ErrInvalid reports a Service built on a nil store, client, runner
	// or ownership check.
	ErrInvalid = errors.New("privacy: invalid config")
	// ErrNotFound reports an unknown episode, an unknown or revoked
	// share token, or a published episode with nothing to play yet.
	// Handlers answer 404, which reveals nothing about what exists.
	ErrNotFound = errors.New("privacy: episode not found")
	// ErrNotOwner reports an episode owned by somebody else. Handlers
	// answer the same 404 as an unknown id, so the response never
	// confirms that a private episode exists.
	ErrNotOwner = errors.New("privacy: episode belongs to another owner")
	// ErrNoRender reports a publish for an episode with no finished
	// render. A share link with no audio would promise what it cannot
	// play, so publish refuses until the render lands.
	ErrNoRender = errors.New("privacy: episode has no render yet")
	// ErrDeleteUnconfirmed reports a provider copy that stays readable
	// after delete. The erasure names it stuck instead of reporting it
	// erased.
	ErrDeleteUnconfirmed = errors.New("privacy: transcript still readable")
	// ErrMediaMissing reports a render row whose blob the media index
	// no longer holds. The row and the bytes disagree, so publish stops
	// instead of opening a link with dead audio.
	ErrMediaMissing = errors.New("privacy: render blob is missing")
)

// BlobStore deletes media blobs by id. The media store implements it.
// A missing id reports an error matching its not found sentinel, which
// the blob target counts as confirmed.
type BlobStore interface {
	// Delete removes the blob stored under id.
	Delete(ctx context.Context, id string) error
}

// SessionClient ends provider voice sessions. The provider session
// client implements it. An already ended session reports success with
// Deleted false, which the session target counts as confirmed.
type SessionClient interface {
	// TerminateSession deletes the provider record for a session id.
	TerminateSession(ctx context.Context, sessionID string) (assemblyai.TerminateResult, error)
}

// TranscriptClient removes provider batch transcripts. The provider
// batch client implements it. A fetch that reads deleted confirms the
// delete, because the provider keeps soft deleted copies readable.
type TranscriptClient interface {
	// Delete removes one transcript by id.
	Delete(ctx context.Context, id string) error
	// Get fetches one transcript with its deletion mark.
	Get(ctx context.Context, id string) (assemblyai.Transcript, error)
}

// OwnerCheck reports whether the request owns ownerID. The guest
// session service implements it by reading the request context the
// middleware set.
type OwnerCheck interface {
	// Owns reports whether the request user owns ownerID.
	Owns(ctx context.Context, ownerID string) bool
}

// Config configures a Service. DB is the shared diary handle. Media
// deletes media blobs. CoverDir holds one cover file per episode named
// for its episode id. Sessions ends provider sessions. Transcripts
// removes provider batch copies. Runner carries the erasure jobs, and
// its erase kind must be registered from Eraser before it opens, or a
// restart leaves an erase interrupted. Owns checks episode ownership
// per request.
type Config struct {
	DB          *sqlite.DB
	Media       BlobStore
	CoverDir    string
	Sessions    SessionClient
	Transcripts TranscriptClient
	Runner      *job.Runner
	Owns        OwnerCheck
}

// Service publishes, unpublishes, shares and erases episodes. Create it
// with New, because the zero value holds no store and no eraser. A
// Service is safe for concurrent use.
type Service struct {
	db          *sqlite.DB
	media       BlobStore
	coverDir    string
	sessions    SessionClient
	transcripts TranscriptClient
	runner      *job.Runner
	owns        OwnerCheck
	eraser      *erase.Eraser
}

// New validates cfg and returns the Service with its eraser. Register
// the returned Eraser kind under the erasure kind name before the
// runner opens, because a kind that declares no resume work never
// resumes after a restart.
func New(cfg Config) (*Service, error) {
	if cfg.DB == nil {
		return nil, fmt.Errorf("%w: database must not be nil", ErrInvalid)
	}
	if cfg.Media == nil {
		return nil, fmt.Errorf("%w: media store must not be nil", ErrInvalid)
	}
	if cfg.CoverDir == "" {
		return nil, fmt.Errorf("%w: cover directory must not be empty", ErrInvalid)
	}
	if cfg.Sessions == nil {
		return nil, fmt.Errorf("%w: session client must not be nil", ErrInvalid)
	}
	if cfg.Transcripts == nil {
		return nil, fmt.Errorf("%w: transcript client must not be nil", ErrInvalid)
	}
	if cfg.Runner == nil {
		return nil, fmt.Errorf("%w: job runner must not be nil", ErrInvalid)
	}
	if cfg.Owns == nil {
		return nil, fmt.Errorf("%w: ownership check must not be nil", ErrInvalid)
	}
	svc := &Service{
		db:          cfg.DB,
		media:       cfg.Media,
		coverDir:    cfg.CoverDir,
		sessions:    cfg.Sessions,
		transcripts: cfg.Transcripts,
		runner:      cfg.Runner,
		owns:        cfg.Owns,
	}
	eraser, err := erase.New(svc.source, erase.Config{})
	if err != nil {
		return nil, fmt.Errorf("privacy: open eraser: %w", err)
	}
	svc.eraser = eraser
	return svc, nil
}

// Eraser returns the erasure runner this service starts jobs on.
// Register svc.Eraser().Kind() under the erasure kind name before the
// job runner opens, so a restart resumes an erase left unfinished.
func (s *Service) Eraser() *erase.Eraser { return s.eraser }
