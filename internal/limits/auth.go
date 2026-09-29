// OwnerAuth gates the admin endpoints behind the operator check.
//
// The guest session middleware already resolves who calls, so the
// check reads the resolved user and the identity keys that user holds.
// OperatorAuth allows callers listed in the operators setting and
// refuses anyone else. StubOwnerAuth denies every call and stands in
// until wiring swaps it. The service takes an OwnerAuth and the
// handler wraps every admin route in it, so swapping needs no change
// to the service or the handler.
package limits

import (
	"net/http"

	"github.com/nrynss/keel/wire"
)

// OwnerAuth gates admin endpoints. Implementations wrap the handler and
// answer refusals through the shared error envelope.
type OwnerAuth interface {
	// Authorize wraps next and allows only the owner through.
	Authorize(next http.Handler) http.Handler
}

// StubOwnerAuth denies every admin call. It stands in for the owner
// login until that decision lands. Swapping it needs no change to the
// service or the handler, only a real implementation of OwnerAuth.
type StubOwnerAuth struct{}

// Authorize denies every call with the owner proof still missing. The
// 403 names the missing proof so the page can render the sign-in state
// once the login exists, instead of mistaking denial for a dead route.
func (StubOwnerAuth) Authorize(_ http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = wire.WriteError(w, http.StatusForbidden, CodeOwnerRequired, "the admin page needs the owner login", nil)
	})
}
