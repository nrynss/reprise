package editorial

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/nrynss/keel/id"
)

// Proposal kinds stored in the proposals table. The editor reads these to
// draw the draft, and the render reads the accepted cuts and the cold
// open. Title and show notes ride the same table so every proposal stays
// revertible in one place.
const (
	// KindColdOpen marks the opening span, chosen for delivery.
	KindColdOpen = "cold_open"
	// KindCut marks one removal with its one line reason.
	KindCut = "cut"
	// KindTitle marks the proposed episode title, carried in reason.
	KindTitle = "title"
	// KindShowNotes marks the show notes, carried in reason.
	KindShowNotes = "show_notes"
	// KindCallback marks the unresolved thing, carried in reason with its
	// quote. The memory loop reads the backing mention row, not this.
	KindCallback = "callback"
)

// DecisionAccepted marks a cut applied by default. The editor reverts a
// cut by writing its own decision row; this package never writes another
// value.
const DecisionAccepted = "accepted"

// MentionCallback marks the mention row backing one callback proposal.
// The next opening cites this row, so the spoken callback comes from
// stored data, never from invention.
const MentionCallback = "callback"

// store writes one validated draft in a single transaction. It replaces
// the episode proposals, accepts every cut with a decisions row, backs
// the callback with a mention row and a callbacks row, and sets the
// episode title. A rerun writes the same shape again instead of doubling
// rows, so a repeated call stays safe.
func store(ctx context.Context, db *sql.DB, ownerID, episodeID string, words []word, got draft) error {
	title := got.Title
	if title == "" {
		title = plainTitle(len(words))
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("editorial: store: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM proposals WHERE episode_id = ?", episodeID); err != nil {
		return fmt.Errorf("editorial: store: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM callbacks WHERE episode_id = ?", episodeID); err != nil {
		return fmt.Errorf("editorial: store: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM mentions WHERE episode_id = ? AND kind = ?", episodeID, MentionCallback); err != nil {
		return fmt.Errorf("editorial: store: %w", err)
	}
	insertProposal := func(kind string, start, end int, reason string) (string, error) {
		proposalID, err := id.New()
		if err != nil {
			return "", fmt.Errorf("editorial: store: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO proposals (id, owner_id, episode_id, kind, start_word, end_word, reason) VALUES (?, ?, ?, ?, ?, ?, ?)",
			proposalID, ownerID, episodeID, kind, start, end, reason); err != nil {
			return "", fmt.Errorf("editorial: store: %w", err)
		}
		return proposalID, nil
	}
	if got.ColdOpen != nil {
		if _, err := insertProposal(KindColdOpen, got.ColdOpen[0], got.ColdOpen[1], got.ColdReason); err != nil {
			return err
		}
	}
	for _, cut := range got.Cuts {
		proposalID, err := insertProposal(KindCut, cut.Start, cut.End, cut.Reason)
		if err != nil {
			return err
		}
		decisionID, err := id.New()
		if err != nil {
			return fmt.Errorf("editorial: store: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO decisions (id, owner_id, episode_id, proposal_id, decision) VALUES (?, ?, ?, ?, ?)",
			decisionID, ownerID, episodeID, proposalID, DecisionAccepted); err != nil {
			return fmt.Errorf("editorial: store: %w", err)
		}
	}
	if _, err := insertProposal(KindTitle, 0, 0, title); err != nil {
		return err
	}
	if got.ShowNotes != "" {
		if _, err := insertProposal(KindShowNotes, 0, 0, got.ShowNotes); err != nil {
			return err
		}
	}
	if got.Callback != nil {
		if _, err := insertProposal(KindCallback, got.Callback.Start, got.Callback.Start, got.Callback.Text+" — "+got.Callback.Quote); err != nil {
			return err
		}
		mentionID, err := id.New()
		if err != nil {
			return fmt.Errorf("editorial: store: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO mentions (id, owner_id, episode_id, kind, word_offset, quote) VALUES (?, ?, ?, ?, ?, ?)",
			mentionID, ownerID, episodeID, MentionCallback, got.Callback.Start, got.Callback.Quote); err != nil {
			return fmt.Errorf("editorial: store: %w", err)
		}
		callbackID, err := id.New()
		if err != nil {
			return fmt.Errorf("editorial: store: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO callbacks (id, owner_id, episode_id, mention_id, used) VALUES (?, ?, ?, ?, 0)",
			callbackID, ownerID, episodeID, mentionID); err != nil {
			return fmt.Errorf("editorial: store: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, "UPDATE episodes SET title = ? WHERE id = ?", title, episodeID); err != nil {
		return fmt.Errorf("editorial: store: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("editorial: store: %w", err)
	}
	return nil
}

// plainTitle names the draft when the model sends no usable title. The
// episode still renders under this name.
func plainTitle(words int) string {
	return fmt.Sprintf("Untitled episode (%d words)", words)
}
