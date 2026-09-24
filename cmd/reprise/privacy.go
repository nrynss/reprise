package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/nrynss/keel/erase"
	"github.com/nrynss/keel/job"
	"github.com/nrynss/keel/mediastore"
	"github.com/nrynss/keel/sqlite"
	"github.com/nrynss/reprise/internal/assemblyai"
	"github.com/nrynss/reprise/internal/identity"
	"github.com/nrynss/reprise/internal/privacy"
	"github.com/nrynss/reprise/internal/retention"
	"github.com/nrynss/reprise/internal/settings"
)

// retentionSweepInterval spaces the guest retention sweep. The window
// runs ninety days, so a daily pass catches every expiry with margin.
const retentionSweepInterval = 24 * time.Hour

// privacyServices carries the publish, erase, and retention services
// the kinds hook built. The mount hook binds and serves them, so both
// hooks share one instance each.
type privacyServices struct {
	// episodes serves publish, revoke, share, and erase to owners.
	episodes *privacy.Service
	// sweeps expires idle guests through the episode erasure.
	sweeps *retention.Service
}

// heldPrivacyServices keeps the services the kinds hook built for the
// mount hook. The boot calls kinds before the runner opens and mount
// after, so the holder bridges the two calls.
var heldPrivacyServices = struct {
	mu       sync.Mutex
	services *privacyServices
}{}

// holdPrivacyServices stores the built services for the mount hook. A
// later build replaces them, so tests start each case from a build of
// their own.
func holdPrivacyServices(services *privacyServices) {
	heldPrivacyServices.mu.Lock()
	defer heldPrivacyServices.mu.Unlock()
	heldPrivacyServices.services = services
}

// heldServices returns the built services, or nil when no build ran.
func heldServices() *privacyServices {
	heldPrivacyServices.mu.Lock()
	defer heldPrivacyServices.mu.Unlock()
	return heldPrivacyServices.services
}

// requestOwner checks episode ownership against the request user the
// guest middleware set. A request with no user owns nothing, so
// signed-out visitors fail every owner endpoint with the same 404 as
// a missing episode.
type requestOwner struct{}

// Owns reports whether the request user owns ownerID.
func (requestOwner) Owns(ctx context.Context, ownerID string) bool {
	user, ok := identity.UserFromContext(ctx)
	return ok && ownerID != "" && user.ID == ownerID
}

// privacyKinds returns the job kinds the publish, erase and retention
// work registers. The boot merges them before the runner opens. Bare
// wiring with no stores registers nothing, so hook tests call every
// hook without a database.
func privacyKinds(w kindWiring) (map[string]job.Kind, error) {
	if w.DB == nil || w.Media == nil || w.Settings.MediaDir == "" {
		return nil, nil
	}
	services, err := buildPrivacyServices(w.DB, w.Media, w.Settings)
	if err != nil {
		return nil, fmt.Errorf("reprise: privacy kinds: %w", err)
	}
	holdPrivacyServices(services)
	return map[string]job.Kind{
		erase.KindName:      services.episodes.Eraser().Kind(),
		retention.SweepName: services.sweeps.Kind(),
	}, nil
}

// mountPrivacy mounts the publish, revoke, erase and share routes, and
// starts the retention schedule. The boot calls it after the core
// routes. Bare wiring mounts nothing, so hook tests call every hook
// on a bare mux.
func mountPrivacy(ctx context.Context, w routeWiring) error {
	if w.Mux == nil || w.DB == nil || w.Media == nil || w.Gate == nil ||
		w.Runner == nil || w.Guests == nil || w.Settings.MediaDir == "" {
		return nil
	}
	services := heldServices()
	if services == nil {
		built, err := buildPrivacyServices(w.DB, w.Media, w.Settings)
		if err != nil {
			return fmt.Errorf("reprise: mount privacy: %w", err)
		}
		holdPrivacyServices(built)
		services = built
	}
	if err := services.episodes.BindRunner(w.Runner); err != nil {
		return fmt.Errorf("reprise: mount privacy: %w", err)
	}
	if err := services.sweeps.BindRunner(w.Runner); err != nil {
		return fmt.Errorf("reprise: mount privacy: %w", err)
	}
	inner := services.episodes.Handler()
	owner, err := w.Gate.Protect(w.Outer, w.Guests(inner))
	if err != nil {
		return fmt.Errorf("reprise: mount privacy: protect owner routes: %w", err)
	}
	w.Mux.Handle("POST "+privacy.PatternPublish, owner)
	w.Mux.Handle("DELETE "+privacy.PatternPublish, owner)
	w.Mux.Handle("DELETE "+privacy.PatternEpisode, owner)
	open, err := w.Gate.Protect(w.Outer, inner)
	if err != nil {
		return fmt.Errorf("reprise: mount privacy: protect share routes: %w", err)
	}
	w.Mux.Handle("GET "+privacy.PatternShare, open)
	w.Mux.Handle("GET "+privacy.PatternShareCover, open)
	startRetentionSchedule(ctx, services.sweeps)
	return nil
}

// buildPrivacyServices opens the owner facing erasure service and the
// guest retention service over one database, one media store, and one
// provider key. The cover directory matches the pipeline cover
// directory, so erasure removes the file the cover pass wrote.
func buildPrivacyServices(db *sqlite.DB, media *mediastore.Store, loaded settings.Settings) (*privacyServices, error) {
	apiKey, err := loaded.Secrets.AssemblyAIAPIKey.Reveal()
	if err != nil {
		return nil, fmt.Errorf("reprise: privacy: reveal provider key: %w", err)
	}
	sessions, err := assemblyai.NewSessionsClient(assemblyai.SessionsBaseURL, apiKey, nil)
	if err != nil {
		return nil, fmt.Errorf("reprise: privacy: open session client: %w", err)
	}
	batch, err := assemblyai.NewBatchClient(assemblyai.Config{
		APIKey: apiKey,
		Model:  loaded.TranscriptionModel,
	})
	if err != nil {
		return nil, fmt.Errorf("reprise: privacy: open batch client: %w", err)
	}
	coverDir := filepath.Join(loaded.MediaDir, "covers")
	if err := os.MkdirAll(coverDir, 0o755); err != nil {
		return nil, fmt.Errorf("reprise: privacy: make cover dir: %w", err)
	}
	episodes, err := privacy.New(privacy.Config{
		DB:          db,
		Media:       media,
		CoverDir:    coverDir,
		Sessions:    sessions,
		Transcripts: batch,
		Owns:        requestOwner{},
	})
	if err != nil {
		return nil, fmt.Errorf("reprise: privacy: open erasure service: %w", err)
	}
	sweeps, err := retention.New(retention.Config{
		DB:          db,
		Media:       media,
		CoverDir:    coverDir,
		Sessions:    sessions,
		Transcripts: batch,
	})
	if err != nil {
		return nil, fmt.Errorf("reprise: privacy: open retention service: %w", err)
	}
	return &privacyServices{episodes: episodes, sweeps: sweeps}, nil
}

// startRetentionSchedule sweeps expired guests once at boot and then
// on an interval until ctx ends. A start failure logs, and the next
// tick retries, so one provider outage never stops the schedule.
func startRetentionSchedule(ctx context.Context, sweeps *retention.Service) {
	go func() {
		if _, err := sweeps.Sweep(ctx); err != nil {
			log.Printf("reprise retention: first sweep: %v", err)
		}
		tick := time.NewTicker(retentionSweepInterval)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				if _, err := sweeps.Sweep(ctx); err != nil {
					log.Printf("reprise retention: sweep: %v", err)
				}
			}
		}
	}()
}
