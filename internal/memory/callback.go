// Package memory callback selection chooses the next episode opening.
//
// Every opening cites a stored mention with its episode and offset. The
// selector reads open commitments, recurring names, and circled topics
// from the index, then stores its pick as a callbacks row pointing at
// the source mention. The live session reads the oldest unused row, so
// speech repeats stored rows and never invents content.
package memory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/nrynss/keel/id"
)

// CallbackKind names the index thread behind one opening choice. The
// kind records which tier won, so later reads can explain the pick.
type CallbackKind string

// Callback kinds, in priority order. An open commitment leads, a
// recurring person follows, and a circled topic closes the list.
const (
	// CallbackCommitment cites an open commitment the speaker still owes.
	CallbackCommitment CallbackKind = "commitment"
	// CallbackPerson cites a recurring person or place.
	CallbackPerson CallbackKind = "person"
	// CallbackTopic cites a circled topic with no close on record.
	CallbackTopic CallbackKind = "topic"
)

// Selection is one chosen opening. Quote and Number are what the host
// speaks, and MentionID is the stored row behind them.
type Selection struct {
	// Kind names the winning tier.
	Kind CallbackKind
	// CallbackID is the callbacks row holding the pick.
	CallbackID string
	// MentionID is the source mention the opening cites.
	MentionID string
	// EpisodeNumber is the source episode, for speech.
	EpisodeNumber int
	// Quote is the stored wording, as heard.
	Quote string
	// Offset is the rendered word index the mention points at.
	Offset int
	// Planted reports whether the pick honors the editorial planted row.
	Planted bool
}

// PlantedCallback is one unused editorial planted row joined to its
// source mention. The selector prefers it when it agrees with the
// index, so the model voice and the stored data open as one.
type PlantedCallback struct {
	// CallbackID is the callbacks row id.
	CallbackID string
	// MentionID is the source mention id.
	MentionID string
	// EpisodeNumber is the mention episode, for speech.
	EpisodeNumber int
	// Quote is the stored wording, as heard.
	Quote string
	// Offset is the rendered word index the mention points at.
	Offset int
}

// candidate is one tier entry before the final pick. Key folds the
// quote, so agreement and repeat checks ignore casing and edge marks.
type candidate struct {
	kind    CallbackKind
	quote   string
	key     string
	number  int
	offset  int
	mention string
}

// Select chooses the next episode opening and stores it.
//
// EpisodeID names the just finished episode the pick is stored under,
// mirroring the editorial pass which plants under the episode it read.
// Priority runs open commitment first, recurring person second, and
// circled topic third. The editorial planted row wins when it agrees
// with the index, meaning it points at the same mention row or its
// quote folds to the same key as a candidate. The pick never repeats
// the most recently stored callback by folded quote. A nil selection
// with no error means the season holds nothing worth opening on, and
// no row is stored.
//
// The caller marks the row used through MarkUsed after the host speaks
// the greeting. Select itself leaves the row unused, so a crash before
// the greeting never loses the opening.
func Select(ctx context.Context, db *sql.DB, ownerID, episodeID string) (*Selection, error) {
	if db == nil || ownerID == "" || episodeID == "" {
		return nil, fmt.Errorf("memory: select callback: %w", ErrInvalid)
	}
	candidates, err := rankCandidates(ctx, db, ownerID)
	if err != nil {
		return nil, err
	}
	planted, err := oldestPlanted(ctx, db, ownerID)
	if err != nil {
		return nil, err
	}
	lastKey, err := lastChoiceKey(ctx, db, ownerID, episodeID)
	if err != nil {
		return nil, err
	}
	if planted != nil {
		if winner, found := agree(candidates, planted); found && NormalizeName(winner.quote) != lastKey {
			if err := keepPlanted(ctx, db, ownerID, episodeID, planted); err != nil {
				return nil, err
			}
			return &Selection{
				Kind:          winner.kind,
				CallbackID:    planted.CallbackID,
				MentionID:     planted.MentionID,
				EpisodeNumber: planted.EpisodeNumber,
				Quote:         planted.Quote,
				Offset:        planted.Offset,
				Planted:       true,
			}, nil
		}
	}
	for _, item := range candidates {
		if item.key == "" || item.key == lastKey {
			continue
		}
		stored, err := storeChoice(ctx, db, ownerID, episodeID, item)
		if err != nil {
			return nil, err
		}
		return stored, nil
	}
	return nil, nil
}

// MarkUsed flags one callbacks row as spoken. The write scopes to the
// owner, so one owner never spends another owner opening. An unknown id
// fails with ErrNotFound instead of a driver error.
func MarkUsed(ctx context.Context, db *sql.DB, ownerID, callbackID string) error {
	if db == nil || ownerID == "" || callbackID == "" {
		return fmt.Errorf("memory: mark callback used: %w", ErrInvalid)
	}
	result, err := db.ExecContext(ctx,
		"UPDATE callbacks SET used = 1 WHERE id = ? AND owner_id = ?", callbackID, ownerID)
	if err != nil {
		return fmt.Errorf("memory: mark callback used: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("memory: mark callback used: %w", err)
	}
	if affected == 0 {
		return fmt.Errorf("memory: mark callback used: %w", ErrNotFound)
	}
	return nil
}

// rankCandidates lists every opening contender in priority order. Open
// commitments come first and oldest, then recurring names by span,
// then circled topics by span. Each entry carries its source mention
// id, so the stored pick cites the row the host speaks.
func rankCandidates(ctx context.Context, db *sql.DB, ownerID string) ([]candidate, error) {
	var out []candidate
	open, err := OpenCommitments(ctx, db, ownerID)
	if err != nil {
		return nil, err
	}
	for _, item := range open {
		out = append(out, candidate{
			kind:    CallbackCommitment,
			quote:   item.Quote,
			key:     NormalizeName(item.Quote),
			number:  item.Number,
			offset:  item.Offset,
			mention: item.ID,
		})
	}
	names, err := RecurringNames(ctx, db, ownerID, 2)
	if err != nil {
		return nil, err
	}
	for _, thread := range names {
		mention, number, offset := oldestHit(ctx, db, ownerID, thread.Key, thread.Episodes)
		if mention == "" {
			continue
		}
		out = append(out, candidate{
			kind:    CallbackPerson,
			quote:   thread.Display,
			key:     thread.Key,
			number:  number,
			offset:  offset,
			mention: mention,
		})
	}
	topics, err := CircledTopics(ctx, db, ownerID)
	if err != nil {
		return nil, err
	}
	for _, topic := range topics {
		mention, number, offset := oldestHit(ctx, db, ownerID, topic.Key, topic.Episodes)
		if mention == "" {
			continue
		}
		out = append(out, candidate{
			kind:    CallbackTopic,
			quote:   topic.Display,
			key:     topic.Key,
			number:  number,
			offset:  offset,
			mention: mention,
		})
	}
	return out, nil
}

// oldestHit resolves the stored mention id behind one thread key. The
// thread hits carry episode and offset but no row id, so this lookup
// reads the oldest matching mention row. It returns empty when no row
// matches, and the caller drops that thread.
func oldestHit(ctx context.Context, db *sql.DB, ownerID, key string, hits []EpisodeHit) (string, int, int) {
	const query = `SELECT m.id, e.number, m.word_offset, m.quote
		FROM mentions AS m
		JOIN episodes AS e ON e.id = m.episode_id
		WHERE m.owner_id = ?
		ORDER BY e.number ASC, m.rowid ASC`
	rows, err := db.QueryContext(ctx, query, ownerID)
	if err != nil {
		return "", 0, 0
	}
	defer rows.Close()
	wantNumbers := make(map[int]bool, len(hits))
	for _, hit := range hits {
		wantNumbers[hit.Number] = true
	}
	for rows.Next() {
		var mentionID, quote string
		var number, offset int
		if err := rows.Scan(&mentionID, &number, &offset, &quote); err != nil {
			return "", 0, 0
		}
		if NormalizeName(quote) != key || !wantNumbers[number] {
			continue
		}
		return mentionID, number, offset
	}
	return "", 0, 0
}

// oldestPlanted returns the oldest unused planted row for the owner,
// joined to its source mention and episode. It returns nil when the
// season holds no unused planted row.
func oldestPlanted(ctx context.Context, db *sql.DB, ownerID string) (*PlantedCallback, error) {
	const query = `SELECT c.id, m.id, m.quote, m.word_offset, e.number
		FROM callbacks AS c
		JOIN mentions AS m ON m.id = c.mention_id AND m.owner_id = c.owner_id
		JOIN episodes AS e ON e.id = m.episode_id
		WHERE c.owner_id = ? AND c.used = 0
		ORDER BY c.rowid ASC
		LIMIT 1`
	var planted PlantedCallback
	err := db.QueryRowContext(ctx, query, ownerID).Scan(
		&planted.CallbackID,
		&planted.MentionID,
		&planted.Quote,
		&planted.Offset,
		&planted.EpisodeNumber,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("memory: load planted callback: %w", err)
	}
	return &planted, nil
}

// agree reports whether the planted row matches the index. Agreement
// means the planted row points at the same mention row as a candidate,
// or its quote folds to the same key as a candidate quote. The first
// agreeing candidate in priority order wins, so the planted pick keeps
// the tier priority behind it.
func agree(candidates []candidate, planted *PlantedCallback) (candidate, bool) {
	plantedKey := NormalizeName(planted.Quote)
	for _, item := range candidates {
		if planted.MentionID == item.mention {
			return item, true
		}
		if plantedKey != "" && plantedKey == item.key {
			return item, true
		}
	}
	return candidate{}, false
}

// lastChoiceKey returns the folded quote of the most recently stored
// callbacks row under any other episode. Rows stored under the current
// episode never count, so the editorial plant for this episode cannot
// block itself. An empty season returns empty with no error, so the
// first pick never excludes anything.
func lastChoiceKey(ctx context.Context, db *sql.DB, ownerID, episodeID string) (string, error) {
	const query = `SELECT m.quote
		FROM callbacks AS c
		JOIN mentions AS m ON m.id = c.mention_id
		WHERE c.owner_id = ? AND c.episode_id != ?
		ORDER BY c.rowid DESC
		LIMIT 1`
	var quote string
	err := db.QueryRowContext(ctx, query, ownerID, episodeID).Scan(&quote)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("memory: load last callback: %w", err)
	}
	return NormalizeName(quote), nil
}

// keepPlanted clears rival unused rows stored under the same episode
// and keeps the agreed planted row. The planted row already cites its
// source mention, so no new row lands.
func keepPlanted(ctx context.Context, db *sql.DB, ownerID, episodeID string, planted *PlantedCallback) error {
	_, err := db.ExecContext(ctx,
		"DELETE FROM callbacks WHERE owner_id = ? AND episode_id = ? AND used = 0 AND id != ?",
		ownerID, episodeID, planted.CallbackID)
	if err != nil {
		return fmt.Errorf("memory: keep planted callback: %w", err)
	}
	return nil
}

// storeChoice clears rival unused rows under the episode and stores
// the pick as a new callbacks row pointing at the source mention. The
// row lands unused, and the host marks it used after speaking it.
func storeChoice(ctx context.Context, db *sql.DB, ownerID, episodeID string, item candidate) (*Selection, error) {
	if item.mention == "" || item.quote == "" {
		return nil, fmt.Errorf("memory: store callback: %w: choice needs its mention and quote", ErrInvalid)
	}
	callbackID, err := id.New()
	if err != nil {
		return nil, fmt.Errorf("memory: store callback: %w", err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("memory: store callback: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		"DELETE FROM callbacks WHERE owner_id = ? AND episode_id = ? AND used = 0",
		ownerID, episodeID); err != nil {
		return nil, fmt.Errorf("memory: store callback: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO callbacks (id, owner_id, episode_id, mention_id, used) VALUES (?, ?, ?, ?, 0)",
		callbackID, ownerID, episodeID, item.mention); err != nil {
		return nil, fmt.Errorf("memory: store callback: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("memory: store callback: %w", err)
	}
	return &Selection{
		Kind:          item.kind,
		CallbackID:    callbackID,
		MentionID:     item.mention,
		EpisodeNumber: item.number,
		Quote:         item.quote,
		Offset:        item.offset,
		Planted:       false,
	}, nil
}
