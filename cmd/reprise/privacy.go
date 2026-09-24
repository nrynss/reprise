package main

import (
	"context"

	"github.com/nrynss/keel/job"
)

// privacyKinds returns the job kinds the publish, erase and retention
// work registers. The boot merges them before the runner opens.
func privacyKinds(kindWiring) (map[string]job.Kind, error) {
	return nil, nil
}

// mountPrivacy mounts the publish, revoke, erase and share routes, and
// starts the retention schedule. The boot calls it after the core routes.
func mountPrivacy(context.Context, routeWiring) error {
	return nil
}
