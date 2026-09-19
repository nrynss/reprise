package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/nrynss/keel/wire"
	"github.com/nrynss/reprise/internal/episode"
)

// sessionStore records the provider close the browser already sent. The
// episode service implements it. The handler declares the seam, so tests
// bind a fake without opening a database.
type sessionStore interface {
	// RecordSessionEnd stores the provider id on one owned diary
	// session and returns its episode.
	RecordSessionEnd(ctx context.Context, ownerID, sessionID, providerSessionID string) (string, error)
}

var _ sessionStore = (*episode.Service)(nil)

// SessionEnd serves the session end route. Create it with NewSessionEnd,
// because the zero value holds no store. Mount wires it under the end
// table pattern behind the spend gate and the guest middleware.
type SessionEnd struct {
	store sessionStore
}

// NewSessionEnd returns the session end route on one handler. A nil store
// answers 500, so wiring faults surface instead of hiding.
func NewSessionEnd(store sessionStore) http.Handler {
	h := &SessionEnd{store: store}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/sessions/{id}/end", h.end)
	return mux
}

// sessionEndRequestJSON carries the provider close the browser already
// sent. The provider id may be empty when the browser ended before the
// session opened, and the settle skips such rows.
type sessionEndRequestJSON struct {
	// ProviderSessionID is the provider session the browser closed.
	ProviderSessionID string `json:"provider_session_id"`
}

// sessionEndJSON echoes the recorded close.
type sessionEndJSON struct {
	// SessionID identifies the diary session row.
	SessionID string `json:"session_id"`
	// EpisodeID identifies the episode the session recorded.
	EpisodeID string `json:"episode_id"`
	// ProviderSessionID is the recorded provider session.
	ProviderSessionID string `json:"provider_session_id"`
}

// end answers POST /api/sessions/{id}/end by recording the provider id
// on the diary session. The browser already ended the provider call, so
// this call only stores what the later settle reads. Unknown and foreign
// sessions both answer 404, and a repeat end stays harmless.
func (h *SessionEnd) end(w http.ResponseWriter, r *http.Request) {
	owner, ok := ownerOf(w, r)
	if !ok {
		return
	}
	if h.store == nil {
		_ = wire.WriteError(w, http.StatusInternalServerError, CodeInternal, "the session store is not wired", nil)
		return
	}
	var body sessionEndRequestJSON
	if !decodeBody(w, r, &body) {
		return
	}
	sessionID := r.PathValue("id")
	episodeID, err := h.store.RecordSessionEnd(r.Context(), owner, sessionID, body.ProviderSessionID)
	if errors.Is(err, episode.ErrNotFound) {
		_ = wire.WriteError(w, http.StatusNotFound, CodeSessionNotFound, "no session lives at this id", nil)
		return
	}
	if err != nil {
		_ = wire.WriteError(w, http.StatusInternalServerError, CodeInternal, "the session end could not be recorded", nil)
		return
	}
	writeJSON(w, http.StatusOK, sessionEndJSON{
		SessionID:         sessionID,
		EpisodeID:         episodeID,
		ProviderSessionID: body.ProviderSessionID,
	})
}
