package memory

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"

	"github.com/nrynss/keel/id"
	"github.com/nrynss/keel/sqlite"
)

// schemaNamespace is the migration ledger namespace this package owns. It
// shares the database file with the diary tables and the Keel stores
// without colliding, because each namespace keeps its own ledger.
const schemaNamespace = "memory"

//go:embed migrations/*.sql
var migrations embed.FS

// Migrate applies the memory schema to db and returns nil. Call it beside
// the store opener at boot, because resolution reads fail without the
// table. A second run changes nothing.
func Migrate(ctx context.Context, db *sqlite.DB) error {
	if db == nil {
		return fmt.Errorf("memory: migrate: %w: database must not be nil", ErrInvalid)
	}
	schema, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return fmt.Errorf("memory: migrate: %w", err)
	}
	if err := sqlite.Migrate(ctx, db, schemaNamespace, schema); err != nil {
		return fmt.Errorf("memory: migrate: %w", err)
	}
	return nil
}

// StoreResolution closes one commitment with one evidence quote in one
// transaction. The evidence lands as a doing mention on the later
// episode, and the resolution row links it to the commitment. A second
// close for the same commitment replaces the first, so rejudging one
// episode never stacks outcomes.
func StoreResolution(ctx context.Context, db *sql.DB, ownerID, commitmentID, episodeID, evidence string, offset int) error {
	if db == nil || ownerID == "" || commitmentID == "" || episodeID == "" || evidence == "" || offset < 0 {
		return fmt.Errorf("memory: store resolution: %w", ErrInvalid)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("memory: store resolution: %w", err)
	}
	defer tx.Rollback()
	var commitmentEpisodeID string
	err = tx.QueryRowContext(ctx,
		"SELECT episode_id FROM mentions WHERE id = ? AND owner_id = ? AND kind = ?",
		commitmentID, ownerID, CommitmentKind).Scan(&commitmentEpisodeID)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("memory: store resolution: %w: unknown commitment", ErrInvalid)
	}
	var commitmentNumber int
	err = tx.QueryRowContext(ctx,
		"SELECT number FROM episodes WHERE id = ?", commitmentEpisodeID).Scan(&commitmentNumber)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("memory: store resolution: %w: commitment episode missing", ErrInvalid)
	}
	if err != nil {
		return fmt.Errorf("memory: store resolution: %w", err)
	}
	var evidenceNumber int
	err = tx.QueryRowContext(ctx,
		"SELECT number FROM episodes WHERE id = ?", episodeID).Scan(&evidenceNumber)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("memory: store resolution: %w: unknown evidence episode", ErrInvalid)
	}
	if err != nil {
		return fmt.Errorf("memory: store resolution: %w", err)
	}
	if evidenceNumber <= commitmentNumber {
		return fmt.Errorf("memory: store resolution: %w: evidence must land on a later episode", ErrInvalid)
	}
	var oldEvidence string
	err = tx.QueryRowContext(ctx,
		"SELECT evidence_mention_id FROM resolutions WHERE owner_id = ? AND commitment_mention_id = ?",
		ownerID, commitmentID).Scan(&oldEvidence)
	switch {
	case err == nil:
		if _, err := tx.ExecContext(ctx, "DELETE FROM resolutions WHERE owner_id = ? AND commitment_mention_id = ?",
			ownerID, commitmentID); err != nil {
			return fmt.Errorf("memory: store resolution: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM mentions WHERE id = ? AND owner_id = ?",
			oldEvidence, ownerID); err != nil {
			return fmt.Errorf("memory: store resolution: %w", err)
		}
	case err == sql.ErrNoRows:
	default:
		return fmt.Errorf("memory: store resolution: %w", err)
	}
	evidenceID, err := id.New()
	if err != nil {
		return fmt.Errorf("memory: store resolution: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO mentions (id, owner_id, episode_id, kind, word_offset, quote) VALUES (?, ?, ?, ?, ?, ?)",
		evidenceID, ownerID, episodeID, DoingKind, offset, evidence); err != nil {
		return fmt.Errorf("memory: store resolution: %w", err)
	}
	resolutionID, err := id.New()
	if err != nil {
		return fmt.Errorf("memory: store resolution: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO resolutions (id, owner_id, commitment_mention_id, evidence_mention_id) VALUES (?, ?, ?, ?)",
		resolutionID, ownerID, commitmentID, evidenceID); err != nil {
		return fmt.Errorf("memory: store resolution: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("memory: store resolution: %w", err)
	}
	return nil
}

// resolutionKeys returns the commitment ids carrying a close on record.
func resolutionKeys(ctx context.Context, db *sql.DB, ownerID string) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx,
		"SELECT commitment_mention_id FROM resolutions WHERE owner_id = ?", ownerID)
	if err != nil {
		return nil, fmt.Errorf("memory: load resolution keys: %w", err)
	}
	defer rows.Close()
	out := make(map[string]bool)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("memory: load resolution keys: %w", err)
		}
		out[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("memory: load resolution keys: %w", err)
	}
	return out, nil
}

// resolvedCommitmentKeys returns the normalised quotes of closed
// commitments. Circled topics matching one drop out, because the speaker
// already reported an outcome for them.
func resolvedCommitmentKeys(ctx context.Context, db *sql.DB, ownerID string) (map[string]bool, error) {
	const query = `SELECT m.quote
		FROM resolutions AS r
		JOIN mentions AS m ON m.id = r.commitment_mention_id
		WHERE r.owner_id = ?`
	rows, err := db.QueryContext(ctx, query, ownerID)
	if err != nil {
		return nil, fmt.Errorf("memory: load resolved quotes: %w", err)
	}
	defer rows.Close()
	out := make(map[string]bool)
	for rows.Next() {
		var quote string
		if err := rows.Scan(&quote); err != nil {
			return nil, fmt.Errorf("memory: load resolved quotes: %w", err)
		}
		if key := NormalizeName(quote); key != "" {
			out[key] = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("memory: load resolved quotes: %w", err)
	}
	return out, nil
}
