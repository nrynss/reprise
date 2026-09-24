package main

import (
	"context"

	"github.com/nrynss/keel/job"
)

// seedKinds returns the job kinds the seeded season registers. The boot
// merges them before the runner opens.
func seedKinds(kindWiring) (map[string]job.Kind, error) {
	return nil, nil
}

// mountSeed syncs the seeded catalog and mounts what copies it to a new
// guest. The boot calls it after the core routes, and a failure here
// refuses the boot.
func mountSeed(context.Context, routeWiring) error {
	return nil
}
