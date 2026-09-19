// Drops for seeded copies.
//
// Drop removes one visitor copy and nothing else. The episode row goes,
// and its delete cascades to the words, mentions, and callbacks beneath
// it. Catalog blobs stay, because copies reference them without owning
// them. Other visitors stay, because every delete scopes to one owner.
// The receipt stays, so the seed never comes back. Recorded episodes
// refuse with ErrNotSeeded and keep their erasure path.

package seed

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Drop removes the seeded episodeID owned by userID. An unknown id or
// another owner id reports ErrNotFound. A recorded episode reports
// ErrNotSeeded, so the caller sends it through the erasure fan-out.
func (s *Service) Drop(ctx context.Context, userID, episodeID string) error {
	if userID == "" || episodeID == "" {
		return wrapInvalid("user id and episode id must not be empty")
	}
	var seeded int
	err := s.db.Reader().QueryRowContext(ctx,
		"SELECT seeded FROM episodes WHERE id = ? AND owner_id = ?", episodeID, userID).Scan(&seeded)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("seed: drop episode %s: %w", episodeID, ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("seed: drop episode %s: %w", episodeID, err)
	}
	if seeded == 0 {
		return fmt.Errorf("seed: drop episode %s: %w", episodeID, ErrNotSeeded)
	}
	if _, err := s.db.Writer().ExecContext(ctx,
		"DELETE FROM episodes WHERE id = ? AND owner_id = ?", episodeID, userID); err != nil {
		return fmt.Errorf("seed: drop episode %s: %w", episodeID, err)
	}
	return nil
}

// Seeded lists the seeded episode ids owned by userID, oldest number
// first. One query finds them by the flag, so wiring and sweeps branch
// on it without reading any other table.
func (s *Service) Seeded(ctx context.Context, userID string) ([]string, error) {
	if userID == "" {
		return nil, wrapInvalid("user id must not be empty")
	}
	rows, err := s.db.Reader().QueryContext(ctx,
		"SELECT id FROM episodes WHERE owner_id = ? AND seeded = 1 ORDER BY number ASC", userID)
	if err != nil {
		return nil, fmt.Errorf("seed: list seeded episodes: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var episodeID string
		if err := rows.Scan(&episodeID); err != nil {
			return nil, fmt.Errorf("seed: list seeded episodes: %w", err)
		}
		out = append(out, episodeID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("seed: list seeded episodes: %w", err)
	}
	return out, nil
}
