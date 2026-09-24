package main

import (
	"context"

	"github.com/nrynss/keel/job"
)

// exportKinds returns the job kinds the export bundle registers. The boot
// merges them before the runner opens.
func exportKinds(kindWiring) (map[string]job.Kind, error) {
	return nil, nil
}

// mountExport mounts the export bundle routes. The boot calls it after
// the core routes.
func mountExport(context.Context, routeWiring) error {
	return nil
}
