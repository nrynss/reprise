package render

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/nrynss/keel/id"
	"github.com/nrynss/reprise/internal/editorial"
	"github.com/nrynss/reprise/internal/transcript"
)

// Cut is one accepted removal on the episode clock.
type Cut struct {
	// StartMs is the inclusive removal start in milliseconds.
	StartMs int64
	// EndMs is the exclusive removal end in milliseconds.
	EndMs int64
}

// AcceptedCuts reads the accepted cuts for one episode. Proposals name
// word offsets into the edit timeline load order, and the latest
// decision per proposal decides. A cut counts only while its latest
// decision reads accepted, so a revert removes it from the render.
// Dangling spans pointing outside the timeline drop, because the model
// row and the words can disagree after an edit.
func AcceptedCuts(ctx context.Context, db *sql.DB, episodeID string) ([]Cut, error) {
	if db == nil || episodeID == "" {
		return nil, fmt.Errorf("render: accepted cuts: %w", ErrInvalid)
	}
	words, err := transcript.Load(ctx, db, episodeID)
	if err != nil {
		return nil, fmt.Errorf("render: accepted cuts: %w", err)
	}
	rows, err := db.QueryContext(ctx,
		`SELECT p.start_word, p.end_word FROM proposals p
		 WHERE p.episode_id = ? AND p.kind = ?
		 AND (SELECT d.decision FROM decisions d
		      WHERE d.proposal_id = p.id
		      ORDER BY d.rowid DESC LIMIT 1) = ?
		 ORDER BY p.rowid ASC`, episodeID, editorial.KindCut, editorial.DecisionAccepted)
	if err != nil {
		return nil, fmt.Errorf("render: accepted cuts: %w", err)
	}
	defer rows.Close()
	var out []Cut
	for rows.Next() {
		var start, end int
		if err := rows.Scan(&start, &end); err != nil {
			return nil, fmt.Errorf("render: accepted cuts: %w", err)
		}
		if start < 0 || end < start || end >= len(words) {
			continue
		}
		if words[end].EndMs <= words[start].StartMs {
			continue
		}
		out = append(out, Cut{StartMs: words[start].StartMs, EndMs: words[end].EndMs})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("render: accepted cuts: %w", err)
	}
	return out, nil
}

// ColdOpen reads the opening span for one episode in milliseconds on the
// episode clock. It returns nil when the draft proposes none, or when the
// latest decision on the opening proposal reads reverted, so the episode
// starts at the top. A dangling span drops like a dangling cut.
func ColdOpen(ctx context.Context, db *sql.DB, episodeID string) (*RangeMs, error) {
	if db == nil || episodeID == "" {
		return nil, fmt.Errorf("render: cold open: %w", ErrInvalid)
	}
	words, err := transcript.Load(ctx, db, episodeID)
	if err != nil {
		return nil, fmt.Errorf("render: cold open: %w", err)
	}
	var proposalID string
	var start, end int
	err = db.QueryRowContext(ctx,
		`SELECT id, start_word, end_word FROM proposals
		 WHERE episode_id = ? AND kind = ? ORDER BY rowid DESC LIMIT 1`,
		episodeID, editorial.KindColdOpen).Scan(&proposalID, &start, &end)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("render: cold open: %w", err)
	}
	var latest sql.NullString
	err = db.QueryRowContext(ctx,
		`SELECT decision FROM decisions
		 WHERE proposal_id = ? ORDER BY rowid DESC LIMIT 1`,
		proposalID).Scan(&latest)
	if err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("render: cold open: %w", err)
	}
	if latest.Valid && latest.String == "reverted" {
		return nil, nil
	}
	if start < 0 || end < start || end >= len(words) {
		return nil, nil
	}
	if words[end].EndMs <= words[start].StartMs {
		return nil, nil
	}
	return &RangeMs{Start: words[start].StartMs, End: words[end].EndMs}, nil
}

// StoredRender is one render row the same inputs produced before.
type StoredRender struct {
	// Hash names the inputs that produced it.
	Hash string
	// OpusMediaID is the streaming blob id.
	OpusMediaID string
	// AACMediaID is the export blob id.
	AACMediaID string
	// Loudness is the stored integrated loudness in LUFS.
	Loudness float64
}

// FindRender returns the stored render for the input hash, or nil when
// no run with these inputs finished yet.
func FindRender(ctx context.Context, db *sql.DB, episodeID, hash string) (*StoredRender, error) {
	if db == nil || episodeID == "" || hash == "" {
		return nil, fmt.Errorf("render: find render: %w", ErrInvalid)
	}
	var found StoredRender
	err := db.QueryRowContext(ctx,
		`SELECT input_hash, opus_media_id, aac_media_id, loudness FROM renders
		 WHERE episode_id = ? AND input_hash = ? ORDER BY rowid DESC LIMIT 1`,
		episodeID, hash).Scan(&found.Hash, &found.OpusMediaID, &found.AACMediaID, &found.Loudness)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("render: find render: %w", err)
	}
	return &found, nil
}

// StoreRender records one finished render. A rerun with the same inputs
// finds the row through FindRender instead of writing again.
func StoreRender(ctx context.Context, db *sql.DB, ownerID, episodeID string, res Result) error {
	if db == nil || ownerID == "" || episodeID == "" || res.Hash == "" {
		return fmt.Errorf("render: store render: %w", ErrInvalid)
	}
	if res.OpusMediaID == "" || res.AACMediaID == "" {
		return fmt.Errorf("render: store render: %w", ErrInvalid)
	}
	renderID, err := id.New()
	if err != nil {
		return fmt.Errorf("render: store render: %w", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO renders (id, owner_id, episode_id, input_hash, opus_media_id, aac_media_id, loudness)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		renderID, ownerID, episodeID, res.Hash, res.OpusMediaID, res.AACMediaID, res.Loudness); err != nil {
		return fmt.Errorf("render: store render: %w", err)
	}
	return nil
}
