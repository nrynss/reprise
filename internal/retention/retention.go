// Package retention expires guest data after the owner chosen window.
//
// Guests are real user rows whose last visit the session service stamps on
// every request. The sweep lists guests whose stamp predates the window,
// erases each of their episodes through the episode erasure service, then
// removes their stray media, session rows and user row. Episodes the owner
// kept move to the owner first, so the sweep never sees them. The sweep
// runs as a job, so a restart resumes it from its recorded snapshot.
//
// The window defaults to ninety days. The owner chose that value, and
// wiring passes the settings value or leaves it unset for the default.
package retention

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/nrynss/keel/erase"
	"github.com/nrynss/keel/job"
	"github.com/nrynss/keel/sqlite"
	"github.com/nrynss/reprise/internal/privacy"
)

// SweepName is the job kind the sweep runs under. Register it beside the
// episode erasure kind before the runner opens, so a restart resumes an
// interrupted sweep from its snapshot.
const SweepName = "retention-sweep"

// DefaultWindow is the guest retention window. A guest idle this long gets
// swept. The owner chose ninety days. Wiring passes the settings value, and
// an unset window reads as this default.
const DefaultWindow = 90 * 24 * time.Hour

// snapshotVersion guards the sweep snapshot encoding. Only version one
// exists.
const snapshotVersion = 1

// sweepMaxAttempts caps the attempts one sweep may make across restarts.
// Every restart may resume the work, because deleting repeats safely.
const sweepMaxAttempts = 5

// episodeRetryRounds caps the erase and re-erase rounds one episode gets
// inside one sweep before the sweep fails naming it.
const episodeRetryRounds = 3

// Sentinel errors. Every failure path this package produces wraps one of
// these, so callers branch with errors.Is.
var (
	// ErrInvalid reports a Service built on nil stores, an empty cover
	// directory, or a negative window.
	ErrInvalid = errors.New("retention: invalid config")
	// ErrNotFound reports an unknown episode or an unknown keeper.
	ErrNotFound = errors.New("retention: not found")
	// ErrIncomplete reports a sweep job that ended with guests still
	// owed. The message names the first guest it could not finish.
	ErrIncomplete = errors.New("retention: sweep incomplete")
)

// Config configures a Service. DB is the shared diary handle. Media deletes
// stray blobs. CoverDir holds one cover file per episode. Sessions ends
// provider sessions and Transcripts removes provider batch copies, both
// through the inner erasure service. Window is the retention window, and a
// non-positive window reads as DefaultWindow. Now stamps the cutoff, and
// nil means time.Now.
type Config struct {
	DB          *sqlite.DB
	Media       privacy.BlobStore
	CoverDir    string
	Sessions    privacy.SessionClient
	Transcripts privacy.TranscriptClient
	Window      time.Duration
	Now         func() time.Time
}

// Service sweeps expired guests and keeps episodes for the owner. Create
// it with New, because the zero value holds no store and no runner. A
// Service is safe for concurrent use. The runner field changes only
// through BindRunner under a lock. Each attempt captures the runner
// once and threads it through, so a bind that lands mid-attempt never
// moves the running work.
type Service struct {
	db     *sqlite.DB
	media  privacy.BlobStore
	window time.Duration
	now    func() time.Time
	trust  *allowlist
	inner  *privacy.Service
	mu     sync.RWMutex
	runner *job.Runner
}

// New validates cfg and returns the Service with its sweep kind. It builds
// the inner erasure service on the same stores, trusting only the guests
// the running sweep allowlists, because a background sweep carries no
// request user. Register Kinds before the runner opens, because a kind
// that declares no resume work never resumes after a restart.
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
	if cfg.Window < 0 {
		return nil, fmt.Errorf("%w: window must not be negative", ErrInvalid)
	}
	window := cfg.Window
	if window <= 0 {
		window = DefaultWindow
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	svc := &Service{db: cfg.DB, media: cfg.Media, window: window, now: now, trust: newAllowlist()}
	inner, err := privacy.New(privacy.Config{
		DB:          cfg.DB,
		Media:       cfg.Media,
		CoverDir:    cfg.CoverDir,
		Sessions:    cfg.Sessions,
		Transcripts: cfg.Transcripts,
		Owns:        svc.trust,
	})
	if err != nil {
		return nil, fmt.Errorf("retention: open eraser: %w", err)
	}
	svc.inner = inner
	return svc, nil
}

// BindRunner carries the sweep jobs and the inner erasure jobs on r. Open
// the runner with Kinds registered, then bind it before any sweep starts.
// A nil runner reports ErrInvalid, and a sweep with no runner bound
// reports ErrInvalid too, because the work must land in the job ledger to
// survive a restart.
func (s *Service) BindRunner(r *job.Runner) error {
	if r == nil {
		return fmt.Errorf("retention: bind runner: %w: runner must not be nil", ErrInvalid)
	}
	s.mu.Lock()
	s.runner = r
	s.mu.Unlock()
	return s.inner.BindRunner(r)
}

// runnerOf returns the bound runner, or nil when none is bound. Callers
// capture the result once per attempt and thread it through every step,
// so a running attempt never re-reads the field a later bind replaces.
func (s *Service) runnerOf() *job.Runner {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.runner
}

// Kind returns the job kind that runs sweeps. Register it under SweepName
// before the runner opens, so a restart resumes an interrupted sweep.
func (s *Service) Kind() job.Kind {
	return job.Kind{Idempotent: true, MaxAttempts: sweepMaxAttempts, Resume: s.resume}
}

// Kinds returns every job kind this service needs: the sweep kind under
// SweepName and the inner erasure kind under its own name. Pass the result
// into the runner config, because an erasure kind that declares no resume
// work never resumes after a restart.
func (s *Service) Kinds() map[string]job.Kind {
	return map[string]job.Kind{SweepName: s.Kind(), erase.KindName: s.inner.Eraser().Kind()}
}

// allowlist trusts the guests one sweep run is erasing. The inner erasure
// service checks ownership per episode, and a background sweep carries no
// request user, so the sweep allowlists each expired guest before it
// erases their episodes. Nothing else ever lands in the set.
type allowlist struct {
	mu  sync.Mutex
	ids map[string]bool
}

// newAllowlist returns an empty allowlist.
func newAllowlist() *allowlist { return &allowlist{ids: map[string]bool{}} }

// Owns reports whether ownerID is a guest the running sweep allowlisted.
func (a *allowlist) Owns(_ context.Context, ownerID string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.ids[ownerID]
}

// set replaces the trusted set with ids.
func (a *allowlist) set(ids []string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	next := make(map[string]bool, len(ids))
	for _, id := range ids {
		next[id] = true
	}
	a.ids = next
}
