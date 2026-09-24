package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"

	"github.com/nrynss/keel/gate"
	"github.com/nrynss/keel/job"
	"github.com/nrynss/keel/mediastore"
	"github.com/nrynss/keel/sqlite"
	"github.com/nrynss/reprise/internal/settings"
)

// errDuplicateKind reports two registrations of one job kind name. The
// runner keys kinds by name, so a second registration would silently
// replace the first one's limit and resume policy. The boot refuses
// instead.
var errDuplicateKind = errors.New("reprise: job kind registered twice")

// kindWiring carries what a feature builds its job kinds from. The runner
// is not open yet when the kinds hooks run, so it holds only the stores
// that exist before it.
type kindWiring struct {
	// DB is the one diary database every store shares.
	DB *sqlite.DB
	// Media is the private blob store.
	Media *mediastore.Store
	// Settings is the loaded settings file.
	Settings settings.Settings
}

// routeWiring carries what a feature mounts its routes with. Every field
// is the value the core routes use, so a feature route sits behind the
// same spend gate, outer rule and guest middleware as the rest.
type routeWiring struct {
	// Mux is the process route table.
	Mux *http.ServeMux
	// Gate is the spend gate every mounted route draws on.
	Gate *gate.Gate
	// Outer is the shared budget in front of every mounted route.
	Outer gate.Rule
	// Guests resolves the guest behind each request.
	Guests func(http.Handler) http.Handler
	// DB is the one diary database every store shares.
	DB *sqlite.DB
	// Runner starts jobs of every registered kind.
	Runner *job.Runner
	// Media is the private blob store.
	Media *mediastore.Store
	// Settings is the loaded settings file.
	Settings settings.Settings
}

// feature names one self-wiring part of the binary. Each lives in its own
// file with its two hooks, so wiring a feature edits that file alone.
type feature struct {
	// name labels the feature in boot errors.
	name string
	// kinds returns the job kinds the feature registers. It runs before
	// the runner opens.
	kinds func(kindWiring) (map[string]job.Kind, error)
	// mount registers the feature routes and schedules. It runs after
	// the core routes mount. ctx ends when the process stops serving.
	mount func(context.Context, routeWiring) error
}

// featureKinds collects the job kinds every feature registers. A feature
// hook that fails refuses the boot by the feature name.
func featureKinds(feats []feature, w kindWiring) ([]map[string]job.Kind, error) {
	out := make([]map[string]job.Kind, 0, len(feats))
	for _, f := range feats {
		if f.kinds == nil {
			continue
		}
		kinds, err := f.kinds(w)
		if err != nil {
			return nil, fmt.Errorf("reprise: %s kinds: %w", f.name, err)
		}
		out = append(out, kinds)
	}
	return out, nil
}

// mountFeatures runs every feature mount hook in order. The first failure
// refuses the boot by the feature name.
func mountFeatures(ctx context.Context, feats []feature, w routeWiring) error {
	for _, f := range feats {
		if f.mount == nil {
			continue
		}
		if err := f.mount(ctx, w); err != nil {
			return fmt.Errorf("reprise: mount %s: %w", f.name, err)
		}
	}
	return nil
}

// mergeKinds joins the core kinds with every feature's kinds into one new
// map. A name that appears twice refuses with errDuplicateKind and names
// every clash, so the boot stops before the runner opens.
func mergeKinds(core map[string]job.Kind, extra ...map[string]job.Kind) (map[string]job.Kind, error) {
	out := make(map[string]job.Kind, len(core))
	for name, kind := range core {
		out[name] = kind
	}
	var clashes []string
	for _, kinds := range extra {
		for name, kind := range kinds {
			if _, taken := out[name]; taken {
				clashes = append(clashes, name)
				continue
			}
			out[name] = kind
		}
	}
	if len(clashes) > 0 {
		sort.Strings(clashes)
		return nil, fmt.Errorf("%w: %v", errDuplicateKind, clashes)
	}
	return out, nil
}
