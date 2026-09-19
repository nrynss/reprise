// Ownership wiring for the privacy endpoints.
//
// The endpoints declare the OwnerCheck seam and never read session
// state themselves. This file adapts the guest session service to that
// seam, so wiring passes one value and handlers stay testable behind a
// function.

package privacy

import (
	"context"

	"github.com/nrynss/reprise/internal/identity"
)

// IdentityOwns adapts the guest session service to the OwnerCheck
// seam. Pass its result as Config.Owns at wiring time. A request with
// no session owns nothing, so signed-out visitors fail every owner
// endpoint with the same 404 as a missing episode.
func IdentityOwns(svc *identity.Service) OwnerCheck {
	return identityOwns{svc: svc}
}

// identityOwns carries the guest session service behind OwnerCheck.
type identityOwns struct {
	svc *identity.Service
}

// Owns reports whether the request user owns ownerID.
func (o identityOwns) Owns(ctx context.Context, ownerID string) bool {
	return o.svc.Owns(ctx, ownerID)
}
