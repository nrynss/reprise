// HTTP endpoints for publishing, unpublishing, sharing, and erasing.
//
// The owner endpoints sit behind the guest session middleware at
// wiring time and check ownership per request. A refusal answers 404
// through the shared envelope, whether the episode is missing or owned
// by somebody else, so no probe tells a visitor what exists. The share
// endpoints take no session at all. Screens branch on stable codes and
// never on wording.

package privacy

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/nrynss/keel/wire"
)

// Envelope codes the screens branch on. Codes stay stable, wording
// stays free.
const (
	// CodeNotFound answers a missing episode, a revoked token, and an
	// unpublished link with one shape.
	CodeNotFound = "not_found"
	// CodeNoRender answers a publish with no finished render yet.
	CodeNoRender = "no_render"
	// CodeInternal answers a fault the caller cannot fix.
	CodeInternal = "internal"
	// CodeMethodNotAllowed answers a wrong method on a live route.
	CodeMethodNotAllowed = "method_not_allowed"
)

// Route patterns. API routes stay plural under /api. Mount the handler
// once and route inside it, so a single mount keeps the table exact.
const (
	// PatternPublish opens an episode behind a fresh share token.
	PatternPublish = "/api/episodes/{id}/publish"
	// PatternEpisode erases an episode through the erasure job.
	PatternEpisode = "/api/episodes/{id}"
	// PatternShare resolves one share token to its episode metadata.
	PatternShare = "/api/share/{token}"
	// PatternShareCover serves the cover behind one share token.
	PatternShareCover = "/api/share/{token}/cover"
)

// PublishResponse is the publish body. SharePath is the page the token
// opens, and ShareToken is the token that opens it.
type PublishResponse struct {
	// ShareToken is the fresh token the publish minted.
	ShareToken string `json:"share_token"`
	// SharePath is the page the token opens.
	SharePath string `json:"share_path"`
}

// UnpublishResponse is the unpublish body. ShareToken is the rotated
// token, which opens nothing while the episode stays private.
type UnpublishResponse struct {
	// ShareToken is the rotated token.
	ShareToken string `json:"share_token"`
}

// EraseResponse is the erase body. JobID is the erasure job the caller
// follows to done over the job stream.
type EraseResponse struct {
	// JobID is the erasure job id.
	JobID string `json:"job_id"`
}

// Handler returns every endpoint of this package on one handler. Mount
// it behind the guest session middleware and the spend gate:
//
//	mux.Handle(PatternPublish, svc.Handler())
//	mux.Handle(PatternEpisode, svc.Handler())
//	mux.Handle(PatternShare, svc.Handler())
//	mux.Handle(PatternShareCover, svc.Handler())
//
// The share patterns take no session, so mounting them behind the
// middleware still serves a signed-out visitor. Ownership checks read
// the request context, and a request with no user owns nothing.
func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(PatternPublish, s.servePublish)
	mux.HandleFunc(PatternEpisode, s.serveEpisode)
	mux.HandleFunc(PatternShare, s.serveShare)
	mux.HandleFunc(PatternShareCover, s.serveShareCover)
	return mux
}

// servePublish answers POST with a fresh token and DELETE with a
// revocation. Both need the owner. A missing episode, another owner's
// episode, and a publish with no render each refuse with their code.
func (s *Service) servePublish(w http.ResponseWriter, r *http.Request) {
	episodeID := r.PathValue("id")
	switch r.Method {
	case http.MethodPost:
		token, err := s.Publish(r.Context(), episodeID)
		if err != nil {
			writePrivacyError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, PublishResponse{ShareToken: token, SharePath: "/share/" + token})
	case http.MethodDelete:
		token, err := s.Unpublish(r.Context(), episodeID)
		if err != nil {
			writePrivacyError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, UnpublishResponse{ShareToken: token})
	default:
		writeRefusal(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "publishing uses POST and DELETE")
	}
}

// serveEpisode answers DELETE by starting the erasure and returning
// the job id. The job does the deleting, so the handler never waits on
// provider calls.
func (s *Service) serveEpisode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeRefusal(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "episodes erase with DELETE")
		return
	}
	jobID, err := s.Erase(r.Context(), r.PathValue("id"))
	if err != nil {
		writePrivacyError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, EraseResponse{JobID: jobID})
}

// serveShare answers GET with the metadata behind one token. It takes
// no session. An unknown, revoked, or unpublished token answers the
// same 404 as a missing episode.
func (s *Service) serveShare(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeRefusal(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "share links read with GET")
		return
	}
	share, err := s.LookupShare(r.Context(), r.PathValue("token"))
	if err != nil {
		writePrivacyError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, share)
}

// serveShareCover answers GET with the cover behind one token. It
// resolves the token first, so only a published episode serves bytes.
func (s *Service) serveShareCover(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeRefusal(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "covers read with GET")
		return
	}
	s.ServeCover(w, r, r.PathValue("token"))
}

// writePrivacyError maps package sentinels to refusal codes. Unknown
// and foreign episodes share the 404, a publish with no render reads
// as conflict, and anything else reads as internal.
func writePrivacyError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound) || errors.Is(err, ErrNotOwner):
		writeRefusal(w, http.StatusNotFound, CodeNotFound, "that episode opens nothing")
	case errors.Is(err, ErrNoRender):
		writeRefusal(w, http.StatusConflict, CodeNoRender, "that episode has no finished render yet")
	default:
		writeRefusal(w, http.StatusInternalServerError, CodeInternal, "that request could not finish")
	}
}

// writeRefusal answers through the shared error envelope. Screens
// branch on the code and never on the message.
func writeRefusal(w http.ResponseWriter, status int, code, message string) {
	_ = wire.WriteError(w, status, code, message, nil)
}

// writeJSON answers with one JSON body.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// zeroTime marks cover bytes immutable without a date. The file name
// already carries the episode, so conditional requests ride on length
// alone.
func zeroTime() time.Time { return time.Time{} }
