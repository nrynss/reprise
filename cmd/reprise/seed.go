package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/nrynss/keel/job"
	"github.com/nrynss/keel/wire"
	"github.com/nrynss/reprise/internal/identity"
	"github.com/nrynss/reprise/internal/seed"
)

// seedWelcomeRoute serves the first visit read behind the welcome screen.
// It lives outside the episode table, because the episode list cannot
// mint the copies it lists.
const seedWelcomeRoute = "GET /api/welcome"

// seedKinds returns the job kinds the seeded season registers. The season
// copies no jobs, so it registers none. The boot merges them before the
// runner opens.
func seedKinds(kindWiring) (map[string]job.Kind, error) {
	return nil, nil
}

// mountSeed syncs the season catalog and mounts the first visit read.
// The boot calls it after the core routes, and a failure here refuses
// the boot. A validation failure names the catalog file behind it. An
// empty wiring registers nothing, because the hook test mounts with one
// and expects no registration. The real boot always passes the stores.
func mountSeed(ctx context.Context, w routeWiring) error {
	if w.DB == nil || w.Media == nil || w.Gate == nil || w.Guests == nil {
		return nil
	}
	dir := filepath.Join(w.Settings.DataDir, "season")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("make season catalog: %w", err)
	}
	svc, err := seed.New(ctx, seed.Config{DB: w.DB, Media: w.Media, CatalogDir: dir})
	if err != nil {
		return err
	}
	if _, err := svc.Sync(ctx); err != nil {
		return fmt.Errorf("sync season catalog: %w", err)
	}
	inner := w.Guests(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		serveWelcome(rw, r, svc)
	}))
	protected, err := w.Gate.Protect(w.Outer, inner)
	if err != nil {
		return fmt.Errorf("protect welcome read: %w", err)
	}
	w.Mux.Handle(seedWelcomeRoute, protected)
	return nil
}

// welcomeTeaserJSON is the episode the welcome screen opens on.
type welcomeTeaserJSON struct {
	// EpisodeNumber orders the teaser in its season.
	EpisodeNumber int `json:"episode_number"`
	// Title names the teaser episode.
	Title string `json:"title"`
	// LineA is the first mention quote behind the teaser.
	LineA string `json:"line_a"`
	// LineB is the second mention quote, or empty.
	LineB string `json:"line_b"`
	// AudioURL is the render audio route, or empty without a render.
	AudioURL string `json:"audio_url"`
}

// welcomeJSON answers the first visit read. Mode is seeded while the
// catalog holds an episode, and empty otherwise. Episodes counts the
// rows behind the teaser. Teaser stays out of an empty season.
type welcomeJSON struct {
	// Mode is seeded or empty.
	Mode string `json:"mode"`
	// Episodes counts the season behind the teaser.
	Episodes int `json:"episodes"`
	// Teaser is the episode the screen opens on, or nil while empty.
	Teaser *welcomeTeaserJSON `json:"teaser,omitempty"`
}

// serveWelcome syncs the catalog, hands the guest their copy once, and
// answers the season behind the welcome screen. Owners hold no copies,
// so they read the catalog. The catalog import runs on every read, so
// files the operator adds reach visitors without a restart.
func serveWelcome(w http.ResponseWriter, r *http.Request, svc *seed.Service) {
	user, ok := identity.UserFromContext(r.Context())
	if !ok || user.ID == "" {
		_ = wire.WriteError(w, http.StatusUnauthorized, "session_required", "this call needs a guest session", nil)
		return
	}
	ctx := r.Context()
	if _, err := svc.Sync(ctx); err != nil {
		log.Printf("reprise: sync season catalog on welcome read: %v", err)
		_ = wire.WriteError(w, http.StatusInternalServerError, "internal_error", "the season catalog could not be read", nil)
		return
	}
	if _, err := svc.EnsureCopy(ctx, user.ID); err != nil {
		log.Printf("reprise: copy season for welcome read: %v", err)
		_ = wire.WriteError(w, http.StatusInternalServerError, "internal_error", "the season could not be copied", nil)
		return
	}
	state, err := svc.Welcome(ctx, user.ID)
	if err != nil {
		log.Printf("reprise: read season for welcome read: %v", err)
		_ = wire.WriteError(w, http.StatusInternalServerError, "internal_error", "the season could not be read", nil)
		return
	}
	out := welcomeJSON{Mode: "empty"}
	if state.Number != 0 {
		teaser := &welcomeTeaserJSON{
			EpisodeNumber: state.Number,
			Title:         state.Title,
			LineA:         state.FirstLine,
			LineB:         state.SecondLine,
		}
		if state.AudioBlobID != "" {
			teaser.AudioURL = "/media/" + state.AudioBlobID
		}
		out = welcomeJSON{Mode: "seeded", Episodes: state.Episodes, Teaser: teaser}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(out)
}
