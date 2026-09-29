package identity

import (
	"net/http"

	"github.com/nrynss/keel/wire"
)

// statusJSON answers the session status route. SignedIn reports whether
// the session user holds an email identity. Email carries that address
// when signed in and stays empty otherwise, so a signed-out answer
// reveals nothing.
type statusJSON struct {
	// SignedIn reports the caller holds an email identity.
	SignedIn bool `json:"signed_in"`
	// Email is the caller own address, empty when signed out.
	Email string `json:"email,omitempty"`
}

// StatusHandler serves the session status route on its own handler. The
// boot mounts it beside the sign-in pair, so every status answer runs
// behind the same guest middleware.
func (s *Service) StatusHandler() http.Handler {
	return http.HandlerFunc(s.handleStatus)
}

// handleStatus answers whether the session user holds an email identity.
// A guest answers signed out. A cookie revoked elsewhere mints a fresh
// guest in the middleware, so it answers signed out too. It reads only
// the caller own rows, so it never reveals another address.
func (s *Service) handleStatus(w http.ResponseWriter, r *http.Request) {
	user, _, ok := loginSession(w, r)
	if !ok {
		return
	}
	keys, err := s.IdentitiesForUser(r.Context(), user.ID)
	if err != nil {
		_ = wire.WriteError(w, http.StatusInternalServerError, CodeInternal, "the session status could not be read", nil)
		return
	}
	for _, key := range keys {
		if key.Provider == emailProvider {
			writeLoginJSON(w, http.StatusOK, statusJSON{SignedIn: true, Email: key.Subject})
			return
		}
	}
	writeLoginJSON(w, http.StatusOK, statusJSON{SignedIn: false})
}
