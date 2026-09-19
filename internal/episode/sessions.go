package episode

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// RecordSessionEnd stores the provider close for one diary session the
// owner holds, and returns its episode. The browser already sent the
// close, so this call only records the provider id the later settle
// reads. Unknown and foreign sessions both report ErrNotFound. A repeat
// call with the same ids succeeds, so a retried end stays harmless.
func (s *Service) RecordSessionEnd(ctx context.Context, ownerID, sessionID, providerSessionID string) (string, error) {
	if s == nil || s.db == nil || ownerID == "" || sessionID == "" {
		return "", fmt.Errorf("episode: record session end %q: %w", sessionID, ErrInvalid)
	}
	var episodeID string
	err := s.db.Reader().QueryRowContext(ctx,
		"SELECT episode_id FROM sessions WHERE id = ? AND owner_id = ?", sessionID, ownerID).Scan(&episodeID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("episode: record session end %q: %w", sessionID, ErrNotFound)
	}
	if err != nil {
		return "", fmt.Errorf("episode: record session end %q: %w", sessionID, err)
	}
	if _, err := s.db.Writer().ExecContext(ctx,
		"UPDATE sessions SET provider_session_id = ? WHERE id = ? AND owner_id = ?",
		providerSessionID, sessionID, ownerID); err != nil {
		return "", fmt.Errorf("episode: record session end %q: %w", sessionID, err)
	}
	return episodeID, nil
}
