package episode

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
)

// ErrProviderConflict reports a provider id report the server cannot
// honour. The id has a bad shape, the row already holds a different one,
// or another session row already holds this one. An empty id and a repeat
// of the stored id still succeed.
var ErrProviderConflict = errors.New("episode: provider session conflict")

// providerSessionIDPattern matches the provider session ids the provider
// mints. Anything else never names a real call, so the server refuses it
// instead of storing a value no settle can read.
var providerSessionIDPattern = regexp.MustCompile(`^sess_[0-9a-f]{32}$`)

// RecordSessionEnd stores the provider close for one diary session the
// owner holds. It returns the episode id and the provider id the row
// holds after the call. An empty provider id does not replace a stored
// one. Unknown and foreign sessions both report ErrNotFound. A repeat
// with the same id succeeds, so a retried end stays harmless. A bad
// shape, a different id over a stored one, or an id another session row
// already holds reports ErrProviderConflict, so one report can never
// steal or rewrite a call it does not name.
func (s *Service) RecordSessionEnd(ctx context.Context, ownerID, sessionID, providerSessionID string) (string, string, error) {
	if s == nil || s.db == nil || ownerID == "" || sessionID == "" {
		return "", "", fmt.Errorf("episode: record session end %q: %w", sessionID, ErrInvalid)
	}
	var episodeID, stored string
	err := s.db.Reader().QueryRowContext(ctx,
		"SELECT episode_id, provider_session_id FROM sessions WHERE id = ? AND owner_id = ?",
		sessionID, ownerID).Scan(&episodeID, &stored)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", fmt.Errorf("episode: record session end %q: %w", sessionID, ErrNotFound)
	}
	if err != nil {
		return "", "", fmt.Errorf("episode: record session end %q: %w", sessionID, err)
	}
	if providerSessionID == "" || providerSessionID == stored {
		return episodeID, stored, nil
	}
	if !providerSessionIDPattern.MatchString(providerSessionID) {
		return "", "", fmt.Errorf("episode: record session end %q: %w", sessionID, ErrProviderConflict)
	}
	if stored != "" {
		return "", "", fmt.Errorf("episode: record session end %q: %w", sessionID, ErrProviderConflict)
	}
	var holder string
	err = s.db.Reader().QueryRowContext(ctx,
		"SELECT id FROM sessions WHERE provider_session_id = ? AND id != ?",
		providerSessionID, sessionID).Scan(&holder)
	if err == nil {
		return "", "", fmt.Errorf("episode: record session end %q: %w", sessionID, ErrProviderConflict)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", "", fmt.Errorf("episode: record session end %q: %w", sessionID, err)
	}
	if _, err := s.db.Writer().ExecContext(ctx,
		"UPDATE sessions SET provider_session_id = ? WHERE id = ? AND owner_id = ?",
		providerSessionID, sessionID, ownerID); err != nil {
		return "", "", fmt.Errorf("episode: record session end %q: %w", sessionID, err)
	}
	return episodeID, providerSessionID, nil
}
