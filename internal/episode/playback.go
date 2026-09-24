package episode

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/nrynss/reprise/internal/analysis"
	"github.com/nrynss/reprise/internal/transcript"
)

// EditWord is one stored word. Edit words sit on the episode clock and
// rendered words sit on the render clock. Start and End are seconds,
// converted from the stored milliseconds.
type EditWord struct {
	// Text is the word as stored.
	Text string
	// Start is the word start in seconds.
	Start float64
	// End is the word end in seconds.
	End float64
}

// EditWords returns edit-source words for an episode the owner holds,
// oldest start first. Other sources stay out, so proposal indexes keep
// their places. Unknown and foreign episodes report ErrNotFound. An
// episode with no edit words returns an empty slice.
func (s *Service) EditWords(ctx context.Context, ownerID, episodeID string) ([]EditWord, error) {
	return s.storedWords(ctx, ownerID, episodeID, transcript.SourceEdit)
}

// RenderedWords returns the words analysis transcribed from the rendered
// file, oldest start first, on the render clock. The player loads that
// file, so these words follow it with no cut arithmetic. An episode whose
// analysis stored no words returns an empty slice. Unknown and foreign
// episodes report ErrNotFound.
func (s *Service) RenderedWords(ctx context.Context, ownerID, episodeID string) ([]EditWord, error) {
	return s.storedWords(ctx, ownerID, episodeID, analysis.SourceRendered)
}

// storedWords reads one word source for an episode the owner holds.
func (s *Service) storedWords(ctx context.Context, ownerID, episodeID, source string) ([]EditWord, error) {
	if s == nil || s.db == nil || ownerID == "" || episodeID == "" {
		return nil, fmt.Errorf("episode: %s words %q: %w", source, episodeID, ErrInvalid)
	}
	if _, err := s.Get(ctx, ownerID, episodeID); err != nil {
		return nil, err
	}
	rows, err := s.db.Reader().QueryContext(ctx,
		`SELECT text, start_ms, end_ms FROM words
		 WHERE episode_id = ? AND owner_id = ? AND source = ?
		 ORDER BY start_ms ASC, rowid ASC`,
		episodeID, ownerID, source)
	if err != nil {
		return nil, fmt.Errorf("episode: %s words %q: %w", source, episodeID, err)
	}
	defer rows.Close()
	out := make([]EditWord, 0)
	for rows.Next() {
		var word EditWord
		var startMs, endMs int64
		if err := rows.Scan(&word.Text, &startMs, &endMs); err != nil {
			return nil, fmt.Errorf("episode: %s words %q: %w", source, episodeID, err)
		}
		word.Start = float64(startMs) / 1000
		word.End = float64(endMs) / 1000
		out = append(out, word)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("episode: %s words %q: %w", source, episodeID, err)
	}
	return out, nil
}

// StemMediaID returns the stored user stem media id when one exists.
// It returns the host stem when no user stem is stored, and empty when
// neither stem exists. Unknown and foreign episodes report ErrNotFound.
func (s *Service) StemMediaID(ctx context.Context, ownerID, episodeID string) (string, error) {
	if s == nil || s.db == nil || ownerID == "" || episodeID == "" {
		return "", fmt.Errorf("episode: stem media %q: %w", episodeID, ErrInvalid)
	}
	if _, err := s.Get(ctx, ownerID, episodeID); err != nil {
		return "", err
	}
	rows, err := s.db.Reader().QueryContext(ctx,
		`SELECT role, media_id FROM stems WHERE episode_id = ? AND owner_id = ?`,
		episodeID, ownerID)
	if err != nil {
		return "", fmt.Errorf("episode: stem media %q: %w", episodeID, err)
	}
	defer rows.Close()
	var userID, hostID string
	for rows.Next() {
		var role, mediaID string
		if err := rows.Scan(&role, &mediaID); err != nil {
			return "", fmt.Errorf("episode: stem media %q: %w", episodeID, err)
		}
		if mediaID == "" {
			continue
		}
		switch role {
		case transcript.RoleUser:
			if userID == "" {
				userID = mediaID
			}
		case transcript.RoleHost:
			if hostID == "" {
				hostID = mediaID
			}
		}
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("episode: stem media %q: %w", episodeID, err)
	}
	if userID != "" {
		return userID, nil
	}
	return hostID, nil
}

// RenderMediaID returns the newest render opus media id for an episode
// the owner holds. Newest is the highest row id. It returns empty when
// no render exists. Unknown and foreign episodes report ErrNotFound.
func (s *Service) RenderMediaID(ctx context.Context, ownerID, episodeID string) (string, error) {
	if s == nil || s.db == nil || ownerID == "" || episodeID == "" {
		return "", fmt.Errorf("episode: render media %q: %w", episodeID, ErrInvalid)
	}
	if _, err := s.Get(ctx, ownerID, episodeID); err != nil {
		return "", err
	}
	var mediaID string
	err := s.db.Reader().QueryRowContext(ctx,
		`SELECT opus_media_id FROM renders
		 WHERE episode_id = ? AND owner_id = ? AND opus_media_id != ''
		 ORDER BY rowid DESC LIMIT 1`,
		episodeID, ownerID).Scan(&mediaID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("episode: render media %q: %w", episodeID, err)
	}
	return mediaID, nil
}
