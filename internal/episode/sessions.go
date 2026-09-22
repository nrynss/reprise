package episode

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// RecordSessionEnd stores the provider close for one diary session the
// owner holds. It returns the episode id and the provider id the row
// holds after the call. An empty provider id does not replace a stored
// one. Unknown and foreign sessions both report ErrNotFound. A repeat
// with the same id succeeds, so a retried end stays harmless.
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
	if _, err := s.db.Writer().ExecContext(ctx,
		"UPDATE sessions SET provider_session_id = ? WHERE id = ? AND owner_id = ?",
		providerSessionID, sessionID, ownerID); err != nil {
		return "", "", fmt.Errorf("episode: record session end %q: %w", sessionID, err)
	}
	return episodeID, providerSessionID, nil
}
