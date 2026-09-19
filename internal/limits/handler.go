// Admin endpoints for the owner page. Every route sits behind the owner
// auth the service was built with, so the stub denies each one until the
// owner login lands. Wire the result behind the guest session middleware,
// which the stub ignores and the real login reads.
package limits

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/nrynss/keel/cost"
	"github.com/nrynss/keel/wire"
)

// Admin route patterns. Human pages stay singular. API routes stay plural
// under /api. Mount the handler subtree at /api/admin/.
const (
	// PatternLimits answers the caps, the switch, and today's spend.
	PatternLimits = "/api/admin/limits"
	// PatternPause flips the switch at once.
	PatternPause = "/api/admin/limits/pause"
	// PatternOwnerLimit sets one owner's ceiling.
	PatternOwnerLimit = "/api/admin/limits/owner"
)

// LimitsSnapshot is the GET body. Global is today's store-wide spend from
// the Keel ledger. Owner carries one owner's figure when the owner query
// names one, and stays absent otherwise. No figure spans days, because
// the ledger carries no charge timestamps.
type LimitsSnapshot struct {
	// SessionsPaused reports the switch the next mint will read.
	SessionsPaused bool `json:"sessions_paused"`
	// Caps carries the guest caps from settings.
	Caps Caps `json:"caps"`
	// Global carries today's store-wide spend.
	Global Spending `json:"global"`
	// Owner carries one owner's spend when requested. It stays nil otherwise.
	Owner *Spending `json:"owner,omitempty"`
}

// PauseRequest is the POST pause body. One boolean flips the switch at
// once, with no restart between the call and the next mint refusal.
type PauseRequest struct {
	// Paused sets the switch. True stops the next mint.
	Paused bool `json:"paused"`
}

// OwnerLimitRequest is the POST owner body. It sets one owner's ceiling
// in nanodollars, the unit the keyed budget enforces.
type OwnerLimitRequest struct {
	// Owner names the owner whose ceiling changes.
	Owner string `json:"owner"`
	// LimitND is the new ceiling in nanodollars. It must not be negative.
	LimitND int64 `json:"limit_nd"`
}

// Handler returns the admin endpoints wrapped in the owner auth. Mount it
// at /api/admin/ behind the guest session middleware:
//
//	mux.Handle(PatternLimits, svc.Handler())
//	mux.Handle(PatternPause, svc.Handler())
//	mux.Handle(PatternOwnerLimit, svc.Handler())
//
// Each pattern routes inside the one handler, so a single mount per
// pattern keeps the route table exact.
func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(PatternLimits, s.serveLimits)
	mux.HandleFunc(PatternPause, s.servePause)
	mux.HandleFunc(PatternOwnerLimit, s.serveOwnerLimit)
	return s.auth.Authorize(mux)
}

// serveLimits answers the caps, the switch, and today's spend. The owner
// query optionally names an owner for a per-owner figure. An unknown
// owner gets 404, because it simply has not started a session yet.
func (s *Service) serveLimits(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeRefusal(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "the limits page reads with GET")
		return
	}
	paused, err := s.Paused(r.Context())
	if err != nil {
		writeRefusal(w, http.StatusInternalServerError, CodeInternal, "the switch could not be read")
		return
	}
	global, err := s.GlobalSpend(r.Context())
	if err != nil {
		writeRefusal(w, http.StatusInternalServerError, CodeInternal, "today's spend could not be read")
		return
	}
	snapshot := LimitsSnapshot{SessionsPaused: paused, Caps: s.Caps(), Global: global}
	if owner := r.URL.Query().Get("owner"); owner != "" {
		figure, err := s.OwnerSpend(r.Context(), owner)
		if err != nil {
			if errors.Is(err, cost.ErrUnknownOwner) {
				writeRefusal(w, http.StatusNotFound, CodeUnknownOwner, "that owner holds no ceiling yet")
				return
			}
			writeRefusal(w, http.StatusInternalServerError, CodeInternal, "that owner's spend could not be read")
			return
		}
		snapshot.Owner = &figure
	}
	writeJSON(w, http.StatusOK, snapshot)
}

// servePause flips the switch at once. The next mint reads the new value,
// so pausing stops token minting without a restart.
func (s *Service) servePause(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeRefusal(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "the switch flips with POST")
		return
	}
	var request PauseRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeRefusal(w, http.StatusBadRequest, CodeInvalidRequest, "the pause body holds no switch value")
		return
	}
	if err := s.SetPaused(r.Context(), request.Paused); err != nil {
		writeRefusal(w, http.StatusInternalServerError, CodeInternal, "the switch could not be flipped")
		return
	}
	paused, err := s.Paused(r.Context())
	if err != nil {
		writeRefusal(w, http.StatusInternalServerError, CodeInternal, "the switch could not be read back")
		return
	}
	writeJSON(w, http.StatusOK, PauseRequest{Paused: paused})
}

// serveOwnerLimit sets one owner's ceiling. The spend figure subtracts
// from this value on later reads, which is the ceiling the keyed budget
// enforces.
func (s *Service) serveOwnerLimit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeRefusal(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "an owner ceiling sets with POST")
		return
	}
	var request OwnerLimitRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeRefusal(w, http.StatusBadRequest, CodeInvalidRequest, "the owner body holds no ceiling")
		return
	}
	if err := s.SetOwnerLimit(r.Context(), request.Owner, cost.Price(request.LimitND)); err != nil {
		if errors.Is(err, ErrInvalid) {
			writeRefusal(w, http.StatusBadRequest, CodeInvalidRequest, "the owner ceiling needs an owner and a non-negative limit")
			return
		}
		writeRefusal(w, http.StatusInternalServerError, CodeInternal, "the owner ceiling could not be set")
		return
	}
	writeJSON(w, http.StatusOK, struct{}{})
}

// writeRefusal answers through the shared error envelope. Screens branch
// on the code and never on the message.
func writeRefusal(w http.ResponseWriter, status int, code, message string) {
	_ = wire.WriteError(w, status, code, message, nil)
}

// writeJSON answers with a JSON body. It never carries a secret, only
// caps, the switch, and spend figures.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
