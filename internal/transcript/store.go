package transcript

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/nrynss/keel/id"
)

// Stored is one persisted timeline word. The words table carries no
// speaker column, so the speaker lives in the turns rows the live pass
// wrote. Readers join by time when they need styling per speaker.
type Stored struct {
	// ID is the row id Replace minted.
	ID string
	// Text is the word as heard, with its original casing.
	Text string
	// StartMs is the word start in milliseconds on the episode clock.
	StartMs int64
	// EndMs is the word end in milliseconds on the episode clock.
	EndMs int64
}

// Replace swaps the episode edit words for words in one transaction. A rerun
// writes the same rows again instead of doubling them, so a repeated call
// stays safe. Every row carries SourceEdit.
func Replace(ctx context.Context, db *sql.DB, ownerID, episodeID string, words []Word) error {
	if db == nil || ownerID == "" || episodeID == "" {
		return fmt.Errorf("transcript: replace: %w", ErrInvalid)
	}
	for _, w := range words {
		if w.EndMs < w.StartMs {
			return fmt.Errorf("transcript: replace %q: %w", w.Text, ErrOrder)
		}
		if w.Role != RoleUser && w.Role != RoleHost {
			return fmt.Errorf("transcript: replace %q: %w: unknown role", w.Text, ErrInvalid)
		}
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("transcript: replace: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		"DELETE FROM words WHERE episode_id = ? AND source = ?", episodeID, SourceEdit); err != nil {
		return fmt.Errorf("transcript: replace: %w", err)
	}
	for _, w := range words {
		wordID, err := id.New()
		if err != nil {
			return fmt.Errorf("transcript: replace: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO words (id, owner_id, episode_id, text, start_ms, end_ms, source) VALUES (?, ?, ?, ?, ?, ?, ?)",
			wordID, ownerID, episodeID, w.Text, w.StartMs, w.EndMs, SourceEdit); err != nil {
			return fmt.Errorf("transcript: replace: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("transcript: replace: %w", err)
	}
	return nil
}

// Load reads the episode edit words ordered by start. The editor and the
// editorial pass read the same order Merge wrote.
func Load(ctx context.Context, db *sql.DB, episodeID string) ([]Stored, error) {
	if db == nil || episodeID == "" {
		return nil, fmt.Errorf("transcript: load: %w", ErrInvalid)
	}
	rows, err := db.QueryContext(ctx,
		"SELECT id, text, start_ms, end_ms FROM words WHERE episode_id = ? AND source = ? ORDER BY start_ms ASC, rowid ASC",
		episodeID, SourceEdit)
	if err != nil {
		return nil, fmt.Errorf("transcript: load: %w", err)
	}
	defer rows.Close()
	var out []Stored
	for rows.Next() {
		var word Stored
		if err := rows.Scan(&word.ID, &word.Text, &word.StartMs, &word.EndMs); err != nil {
			return nil, fmt.Errorf("transcript: load: %w", err)
		}
		out = append(out, word)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("transcript: load: %w", err)
	}
	return out, nil
}
