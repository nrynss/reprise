package identity

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/nrynss/keel/id"
	"github.com/nrynss/keel/wire"
)

// SignOutOutcome carries a sign-out. UserID is the fresh guest that owns
// the device now. SessionID is the fresh session the handler sets as a
// cookie, and the old session row stays revoked.
type SignOutOutcome struct {
	// UserID is the fresh guest user that owns the device now.
	UserID string
	// SessionID is the fresh session id for the response cookie.
	SessionID string
}

// SignOut revokes one session and mints a fresh guest beside it. The new
// user row and the new session row land in one transaction with the
// revocation, so the device never sits between owners. Guests and
// signed-in callers share this one path and this one answer, so neither
// learns who held an account. The stored identity rows stay untouched,
// and the account keeps working on its other devices.
func (s *Service) SignOut(ctx context.Context, sessionID string) (SignOutOutcome, error) {
	if sessionID == "" {
		return SignOutOutcome{}, fmt.Errorf("%w: session must not be empty", ErrInvalid)
	}
	userID, err := id.New()
	if err != nil {
		return SignOutOutcome{}, fmt.Errorf("identity: sign out: %w", err)
	}
	nextSession, err := id.New()
	if err != nil {
		return SignOutOutcome{}, fmt.Errorf("identity: sign out: %w", err)
	}
	stamp := s.now().Unix()
	tx, err := s.db.Writer().BeginTx(ctx, nil)
	if err != nil {
		return SignOutOutcome{}, fmt.Errorf("identity: sign out: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if _, err := tx.ExecContext(ctx, "INSERT INTO users (id, kind, created_at, last_seen_at) VALUES (?, ?, ?, ?)",
		userID, KindGuest, stamp, stamp); err != nil {
		return SignOutOutcome{}, fmt.Errorf("identity: sign out: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO guest_sessions (id, user_id, created_at, revoked) VALUES (?, ?, ?, 0)",
		nextSession, userID, stamp); err != nil {
		return SignOutOutcome{}, fmt.Errorf("identity: sign out: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		"UPDATE guest_sessions SET revoked = 1 WHERE id = ?", sessionID); err != nil {
		return SignOutOutcome{}, fmt.Errorf("identity: sign out: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return SignOutOutcome{}, fmt.Errorf("identity: sign out: %w", err)
	}
	committed = true
	return SignOutOutcome{UserID: userID, SessionID: nextSession}, nil
}

// SignOutHandler serves the sign-out route on its own handler. The boot
// mounts it beside the sign-in pair, so every login path answers behind
// the same guest middleware.
func (s *Service) SignOutHandler() http.Handler {
	return http.HandlerFunc(s.handleSignout)
}

// handleSignout answers the sign-out route. It revokes the session the
// middleware resolved, mints a fresh guest, sets its cookie, and answers
// 200 with the same body guests and signed-in callers share.
func (s *Service) handleSignout(w http.ResponseWriter, r *http.Request) {
	_, sessionID, ok := loginSession(w, r)
	if !ok {
		return
	}
	outcome, err := s.SignOut(r.Context(), sessionID)
	if err != nil {
		if errors.Is(err, ErrInvalid) {
			_ = wire.WriteError(w, http.StatusBadRequest, CodeInvalidRequest, "this request carries no usable session", nil)
			return
		}
		_ = wire.WriteError(w, http.StatusInternalServerError, CodeInternal, "the sign-out could not complete", nil)
		return
	}
	s.SetSessionCookie(w, outcome.SessionID)
	writeLoginJSON(w, http.StatusOK, loginOKJSON{OK: true})
}
