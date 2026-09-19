package episode

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/nrynss/keel/id"
)

// Decision values the decisions table holds. Accepted applies a proposal
// and reverted drops it. The render reads the latest row per proposal, so
// these literals must match what that pass compares against.
const (
	// DecisionAccepted applies the proposal it names.
	DecisionAccepted = "accepted"
	// DecisionReverted drops the proposal it names.
	DecisionReverted = "reverted"
)

// Proposal is one stored proposal with its latest decision. HasDecision
// stays false while the user never touched the proposal, so the editor
// falls back to the default the draft stored.
type Proposal struct {
	// ID identifies the proposal.
	ID string
	// Kind names the proposal: cut, cold open, title, notes, or callback.
	Kind string
	// StartWord is the first timeline word the proposal covers.
	StartWord int
	// EndWord is the last timeline word the proposal covers.
	EndWord int
	// Reason carries the one line reason, title, notes, or callback text.
	Reason string
	// Decision is the latest decision, or empty while untouched.
	Decision string
	// HasDecision reports a decision row exists for the proposal.
	HasDecision bool
}

// Proposals returns every proposal for an episode the owner holds, oldest
// first, each with its latest decision. Unknown and foreign episodes both
// report ErrNotFound, so callers answer 404 either way.
func (s *Service) Proposals(ctx context.Context, ownerID, episodeID string) ([]Proposal, error) {
	if s == nil || s.db == nil || ownerID == "" || episodeID == "" {
		return nil, fmt.Errorf("episode: proposals %q: %w", episodeID, ErrInvalid)
	}
	if _, err := s.Get(ctx, ownerID, episodeID); err != nil {
		return nil, err
	}
	rows, err := s.db.Reader().QueryContext(ctx,
		`SELECT p.id, p.kind, p.start_word, p.end_word, p.reason,
			(SELECT d.decision FROM decisions d
			 WHERE d.proposal_id = p.id ORDER BY d.rowid DESC LIMIT 1)
		 FROM proposals p WHERE p.episode_id = ? ORDER BY p.rowid ASC`, episodeID)
	if err != nil {
		return nil, fmt.Errorf("episode: proposals %q: %w", episodeID, err)
	}
	defer rows.Close()
	var out []Proposal
	for rows.Next() {
		var p Proposal
		var latest sql.NullString
		if err := rows.Scan(&p.ID, &p.Kind, &p.StartWord, &p.EndWord, &p.Reason, &latest); err != nil {
			return nil, fmt.Errorf("episode: proposals %q: %w", episodeID, err)
		}
		if latest.Valid {
			p.Decision = latest.String
			p.HasDecision = true
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("episode: proposals %q: %w", episodeID, err)
	}
	return out, nil
}

// Decide appends one decision row for a proposal the owner holds. Accept
// and revert are the only values, and every call appends, so the latest
// row always wins and no edit applies without its record. Unknown and
// foreign episodes or proposals both report ErrNotFound.
func (s *Service) Decide(ctx context.Context, ownerID, episodeID, proposalID, decision string) error {
	if s == nil || s.db == nil || ownerID == "" || episodeID == "" || proposalID == "" {
		return fmt.Errorf("episode: decide %q: %w", proposalID, ErrInvalid)
	}
	if decision != DecisionAccepted && decision != DecisionReverted {
		return fmt.Errorf("episode: decide %q to %q: %w", proposalID, decision, ErrInvalid)
	}
	if _, err := s.Get(ctx, ownerID, episodeID); err != nil {
		return err
	}
	var one int
	err := s.db.Reader().QueryRowContext(ctx,
		"SELECT 1 FROM proposals WHERE id = ? AND episode_id = ?", proposalID, episodeID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("episode: decide %q: %w", proposalID, ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("episode: decide %q: %w", proposalID, err)
	}
	decisionID, err := id.New()
	if err != nil {
		return fmt.Errorf("episode: decide %q: %w", proposalID, err)
	}
	if _, err := s.db.Writer().ExecContext(ctx,
		"INSERT INTO decisions (id, owner_id, episode_id, proposal_id, decision) VALUES (?, ?, ?, ?, ?)",
		decisionID, ownerID, episodeID, proposalID, decision); err != nil {
		return fmt.Errorf("episode: decide %q: %w", proposalID, err)
	}
	return nil
}
