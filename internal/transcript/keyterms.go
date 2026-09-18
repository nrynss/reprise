package transcript

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// MaxKeyterms caps boosted names per request. The live session caps at one
// hundred names, so the batch pass boosts the same set.
const MaxKeyterms = 100

// MaxKeytermWords caps words per boosted phrase. Longer phrases never reach
// the provider, which accepts six words at most.
const MaxKeytermWords = 6

// Keyterms reads the owner mention quotes for the batch boost. It returns
// the most recent wording first, deduped case-insensitively. It skips blank
// quotes and phrases over the provider word cap, then keeps the first
// MaxKeyterms.
func Keyterms(ctx context.Context, db *sql.DB, ownerID string) ([]string, error) {
	if db == nil || ownerID == "" {
		return nil, fmt.Errorf("transcript: keyterms: %w", ErrInvalid)
	}
	const query = `SELECT m.quote
		FROM mentions AS m
		JOIN episodes AS e ON e.id = m.episode_id
		WHERE m.owner_id = ?
		ORDER BY e.number DESC, m.rowid DESC`
	rows, err := db.QueryContext(ctx, query, ownerID)
	if err != nil {
		return nil, fmt.Errorf("transcript: keyterms: %w", err)
	}
	defer rows.Close()
	var terms []string
	seen := make(map[string]bool)
	for rows.Next() {
		var quote string
		if err := rows.Scan(&quote); err != nil {
			return nil, fmt.Errorf("transcript: keyterms: %w", err)
		}
		cleaned := cleanMention(quote)
		if cleaned == "" || len(strings.Fields(cleaned)) > MaxKeytermWords {
			continue
		}
		key := strings.ToLower(cleaned)
		if seen[key] {
			continue
		}
		seen[key] = true
		terms = append(terms, cleaned)
		if len(terms) >= MaxKeyterms {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("transcript: keyterms: %w", err)
	}
	return terms, nil
}

// cleanMention collapses one stored excerpt to a single line for boosting.
// It trims surrounding quotes and blanks, so the list carries names rather
// than punctuation.
func cleanMention(quote string) string {
	cleaned := strings.Join(strings.Fields(quote), " ")
	return strings.Trim(cleaned, `"' `)
}
