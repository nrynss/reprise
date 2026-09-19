package episode

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Episode is one episode row the owner holds.
type Episode struct {
	// ID identifies the episode.
	ID string
	// OwnerID identifies the owning user row.
	OwnerID string
	// Number orders the episode within its owner.
	Number int64
	// Title names the episode.
	Title string
	// State is the lifecycle state.
	State State
	// Visibility is private until an explicit publish.
	Visibility string
}

// List returns every episode the owner holds, oldest number first. A
// stranger lists nothing, because the query scopes on the owner.
func (s *Service) List(ctx context.Context, ownerID string) ([]Episode, error) {
	if s == nil || s.db == nil || ownerID == "" {
		return nil, fmt.Errorf("episode: list: %w", ErrInvalid)
	}
	rows, err := s.db.Reader().QueryContext(ctx,
		`SELECT id, owner_id, number, title, state, visibility FROM episodes
		 WHERE owner_id = ? ORDER BY number ASC`, ownerID)
	if err != nil {
		return nil, fmt.Errorf("episode: list: %w", err)
	}
	defer rows.Close()
	var out []Episode
	for rows.Next() {
		var ep Episode
		if err := rows.Scan(&ep.ID, &ep.OwnerID, &ep.Number, &ep.Title, &ep.State, &ep.Visibility); err != nil {
			return nil, fmt.Errorf("episode: list: %w", err)
		}
		out = append(out, ep)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("episode: list: %w", err)
	}
	return out, nil
}

// Get returns one episode when the owner holds it. Unknown and foreign
// ids both report ErrNotFound, so callers answer 404 either way and never
// confirm that a private episode exists.
func (s *Service) Get(ctx context.Context, ownerID, episodeID string) (Episode, error) {
	if s == nil || s.db == nil || ownerID == "" || episodeID == "" {
		return Episode{}, fmt.Errorf("episode: get %q: %w", episodeID, ErrInvalid)
	}
	var ep Episode
	err := s.db.Reader().QueryRowContext(ctx,
		`SELECT id, owner_id, number, title, state, visibility FROM episodes
		 WHERE id = ? AND owner_id = ?`, episodeID, ownerID).Scan(
		&ep.ID, &ep.OwnerID, &ep.Number, &ep.Title, &ep.State, &ep.Visibility)
	if errors.Is(err, sql.ErrNoRows) {
		return Episode{}, fmt.Errorf("episode: get %q: %w", episodeID, ErrNotFound)
	}
	if err != nil {
		return Episode{}, fmt.Errorf("episode: get %q: %w", episodeID, err)
	}
	return ep, nil
}
