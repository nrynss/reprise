package api

import (
	"encoding/json"
	"net/http"

	"github.com/nrynss/keel/wire"
	"github.com/nrynss/reprise/internal/identity"
)

// Envelope codes the episode and session routes answer with. Screens
// branch on these and never on wording.
const (
	// CodeSessionRequired answers a request with no guest behind it.
	CodeSessionRequired = "session_required"
	// CodeEpisodeNotFound answers an unknown episode or one owned by
	// somebody else, so the response never confirms a private row.
	CodeEpisodeNotFound = "episode_not_found"
	// CodeProposalNotFound answers an unknown proposal on an owned
	// episode.
	CodeProposalNotFound = "proposal_not_found"
	// CodeSessionNotFound answers an unknown diary session or one owned
	// by somebody else.
	CodeSessionNotFound = "session_not_found"
	// CodeInvalidRequest answers a malformed body or a value the route
	// cannot honour.
	CodeInvalidRequest = "invalid_request"
	// CodeIllegalTransition answers a mark done on an episode that is
	// not a draft, including a repeat tap on one already rendering.
	CodeIllegalTransition = "illegal_transition"
	// CodeRenderBusy answers a mark done while the render kind runs at
	// capacity. The episode waits in failed for an explicit retry.
	CodeRenderBusy = "render_busy"
	// CodeRenderUnavailable answers a mark done whose render job never
	// started. The episode waits in failed for an explicit retry.
	CodeRenderUnavailable = "render_unavailable"
	// CodeInternal answers a diary or dependency fault.
	CodeInternal = "internal_error"
)

// maxBodyBytes caps one JSON request body. Decision and end bodies carry
// two short strings, so one megabyte leaves wide room.
const maxBodyBytes = 1 << 20

// requestUser returns the guest id the middleware resolved for this
// request. It returns false when no middleware ran, which never happens
// behind Mount.
func requestUser(r *http.Request) (string, bool) {
	user, ok := identity.UserFromContext(r.Context())
	if !ok || user.ID == "" {
		return "", false
	}
	return user.ID, true
}

// writeJSON answers 200 or 202 with one named payload. Every diary read
// travels no-store, so a shared cache never keeps a private row.
func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

// decodeBody reads one JSON body up to the cap. It reports false when the
// body is missing or malformed, and the caller answers invalid request.
func decodeBody(w http.ResponseWriter, r *http.Request, shape any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(shape); err != nil {
		_ = wire.WriteError(w, http.StatusBadRequest, CodeInvalidRequest, "this request carries no usable body", nil)
		return false
	}
	return true
}

// refuseNoUser answers 401 when no guest middleware ran upstream.
func refuseNoUser(w http.ResponseWriter) {
	_ = wire.WriteError(w, http.StatusUnauthorized, CodeSessionRequired, "this call needs a guest session", nil)
}

// ownerOf returns the request guest or answers 401. Handlers call it
// first, so no owned row is read without its owner.
func ownerOf(w http.ResponseWriter, r *http.Request) (string, bool) {
	owner, ok := requestUser(r)
	if !ok {
		refuseNoUser(w)
		return "", false
	}
	return owner, true
}
