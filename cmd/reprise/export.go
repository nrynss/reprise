package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/nrynss/keel/job"
	"github.com/nrynss/keel/sqlite"
	"github.com/nrynss/reprise/internal/export"
	"github.com/nrynss/reprise/internal/settings"
)

// exportServices carries the bundle service the kinds hook built. The
// mount hook serves it, so the holder bridges the two calls.
type exportServices struct {
	// bundles builds and serves one owner held bundle per request.
	bundles *export.Service
}

// heldExportServices keeps the service the kinds hook built for the
// mount hook. The boot calls kinds before the runner opens and mount
// after, so the holder bridges the two calls.
var heldExportServices = struct {
	mu       sync.Mutex
	services *exportServices
}{}

// holdExportServices stores the built service for the mount hook. A
// later build replaces it, so tests start each case from a build of
// their own.
func holdExportServices(services *exportServices) {
	heldExportServices.mu.Lock()
	defer heldExportServices.mu.Unlock()
	heldExportServices.services = services
}

// heldExport returns the built service, or nil when no build ran.
func heldExport() *exportServices {
	heldExportServices.mu.Lock()
	defer heldExportServices.mu.Unlock()
	return heldExportServices.services
}

// exportKinds returns the job kind the export bundle registers. The
// boot merges it before the runner opens, and the merge refuses a name
// the core kinds already carry. Bare wiring with no stores registers
// nothing, so hook tests call every hook without a database.
func exportKinds(w kindWiring) (map[string]job.Kind, error) {
	if w.DB == nil || w.Settings.MediaDir == "" {
		return nil, nil
	}
	svc, err := buildExportService(w.DB, w.Settings)
	if err != nil {
		return nil, fmt.Errorf("reprise: export kinds: %w", err)
	}
	holdExportServices(&exportServices{bundles: svc})
	return map[string]job.Kind{export.KindName: svc.Kind()}, nil
}

// mountExport mounts the export bundle route behind the owner gate and
// the guest middleware. The boot calls it after the core routes. Bare
// wiring mounts nothing, so hook tests call every hook on a bare mux.
// A stranger reads the same 404 as a missing episode, because the
// service checks ownership per request.
func mountExport(_ context.Context, w routeWiring) error {
	if w.Mux == nil || w.DB == nil || w.Gate == nil || w.Guests == nil || w.Settings.MediaDir == "" {
		return nil
	}
	services := heldExport()
	if services == nil {
		built, err := buildExportService(w.DB, w.Settings)
		if err != nil {
			return fmt.Errorf("reprise: mount export: %w", err)
		}
		services = &exportServices{bundles: built}
		holdExportServices(services)
	}
	inner := services.bundles.Handler()
	owner, err := w.Gate.Protect(w.Outer, w.Guests(inner))
	if err != nil {
		return fmt.Errorf("reprise: mount export: protect owner routes: %w", err)
	}
	w.Mux.Handle("GET "+export.PatternExport, owner)
	return nil
}

// buildExportService opens the bundle service over one database, one
// media directory, and one cover directory. The build runs inline in
// the request, so no runner binding travels with it. The cover
// directory matches the pipeline cover directory, so the bundle
// carries the file the cover pass wrote.
func buildExportService(db *sqlite.DB, loaded settings.Settings) (*export.Service, error) {
	coverDir := filepath.Join(loaded.MediaDir, "covers")
	if err := os.MkdirAll(coverDir, 0o755); err != nil {
		return nil, fmt.Errorf("reprise: export: make cover dir: %w", err)
	}
	svc, err := export.New(export.Config{
		DB:       db,
		MediaDir: loaded.MediaDir,
		CoverDir: coverDir,
		Owns:     requestOwner{},
	})
	if err != nil {
		return nil, fmt.Errorf("reprise: export: open bundle service: %w", err)
	}
	return svc, nil
}
