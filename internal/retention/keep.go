// Owner kept episodes.
//
// Keep moves one guest episode to the keeping owner before expiry can
// touch it. The episode row, every content row beneath it, the cover
// row, the resolution links, and every media blob grouped under it
// change owner in one transaction, so the sweep later finds no trace
// of the episode under the guest. The rendered source link names no
// owner, so it follows the episode untouched. The provider session and
// batch transcripts stay named by their own ids, and the sweep never
// inventories them once the episode belongs to the owner. A kept
// episode is therefore the owner copy, and it survives its guest
// expiry.

package retention

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// keptTables names every diary table whose rows carry an episode with an
// owner. Keep repoints all of them, so no row still names the guest after
// the move.
var keptTables = []string{
	"sessions", "stems", "turns", "words", "proposals", "decisions",
	"renders", "analyses", "mentions", "callbacks",
}

// Keep moves episodeID to keeperID and returns at once. The keeper must be
// an existing user, and the episode must exist. Keeping an episode the
// keeper already owns succeeds without changing anything, so a repeated
// keep is safe. The episode takes the next free number under the keeper,
// because numbers stay unique per owner.
func (s *Service) Keep(ctx context.Context, episodeID, keeperID string) error {
	if episodeID == "" || keeperID == "" {
		return fmt.Errorf("retention: keep: %w: empty episode or keeper", ErrInvalid)
	}
	var owner string
	var number int64
	err := s.db.Reader().QueryRowContext(ctx,
		"SELECT owner_id, number FROM episodes WHERE id = ?", episodeID).Scan(&owner, &number)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("retention: keep %s: %w", episodeID, ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("retention: keep %s: %w", episodeID, err)
	}
	if owner == keeperID {
		return nil
	}
	var keeper string
	err = s.db.Reader().QueryRowContext(ctx,
		"SELECT id FROM users WHERE id = ?", keeperID).Scan(&keeper)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("retention: keep %s: %w: unknown keeper", episodeID, ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("retention: keep %s: %w", episodeID, err)
	}
	tx, err := s.db.Writer().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("retention: keep %s: %w", episodeID, err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	var next int64
	err = tx.QueryRowContext(ctx,
		"SELECT COALESCE(MAX(number), 0) + 1 FROM episodes WHERE owner_id = ?", keeperID).Scan(&next)
	if err != nil {
		return fmt.Errorf("retention: keep %s: %w", episodeID, err)
	}
	if _, err := tx.ExecContext(ctx,
		"UPDATE episodes SET owner_id = ?, number = ? WHERE id = ?", keeperID, next, episodeID); err != nil {
		return fmt.Errorf("retention: keep %s: %w", episodeID, err)
	}
	for _, table := range keptTables {
		if _, err := tx.ExecContext(ctx,
			"UPDATE "+table+" SET owner_id = ? WHERE episode_id = ?", keeperID, episodeID); err != nil {
			return fmt.Errorf("retention: keep %s: %w", episodeID, err)
		}
	}
	if _, err := tx.ExecContext(ctx,
		"UPDATE media SET owner = ? WHERE media_group = ?", keeperID, episodeID); err != nil {
		return fmt.Errorf("retention: keep %s: %w", episodeID, err)
	}
	if err := moveCoverRow(ctx, tx, episodeID, keeperID); err != nil {
		return err
	}
	if err := moveResolutionRows(ctx, tx, episodeID, keeperID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("retention: keep %s: %w", episodeID, err)
	}
	committed = true
	return nil
}

// moveCoverRow repoints the cover row of one kept episode at the
// keeper. The cover file already names the episode, so only the owner
// column moves. A database that never stored a cover holds no table,
// and Keep skips it instead of failing the move.
func moveCoverRow(ctx context.Context, tx *sql.Tx, episodeID, keeperID string) error {
	if !tableExists(ctx, tx, "covers") {
		return nil
	}
	if _, err := tx.ExecContext(ctx,
		"UPDATE covers SET owner_id = ? WHERE episode_id = ?", keeperID, episodeID); err != nil {
		return fmt.Errorf("retention: keep %s: %w", episodeID, err)
	}
	return nil
}

// moveResolutionRows repoints the resolution links of one kept episode
// at the keeper. Links carry no episode column, so the move follows
// the commitment mentions of the episode. The evidence mention may
// live on another episode, and the link still moves with its
// commitment. A database with no memory index holds no table, and
// Keep skips it instead of failing the move. The rendered source link
// names no owner, so it needs no move.
func moveResolutionRows(ctx context.Context, tx *sql.Tx, episodeID, keeperID string) error {
	if !tableExists(ctx, tx, "resolutions") {
		return nil
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE resolutions SET owner_id = ? WHERE commitment_mention_id IN
		 (SELECT id FROM mentions WHERE episode_id = ?)`, keeperID, episodeID); err != nil {
		return fmt.Errorf("retention: keep %s: %w", episodeID, err)
	}
	return nil
}

// tableExists reports whether name holds a table in this database. The
// cover and memory tables arrive with their own passes, so Keep skips
// them when those passes never ran instead of failing the whole move.
func tableExists(ctx context.Context, tx *sql.Tx, name string) bool {
	var found string
	err := tx.QueryRowContext(ctx,
		"SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?", name).Scan(&found)
	return err == nil && found == name
}
