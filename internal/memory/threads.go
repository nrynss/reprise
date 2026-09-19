package memory

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
)

// EpisodeHit is one appearance of a thread in one episode. The quote is
// the stored wording, and the offset points at its rendered words, so a
// thread item links to the moment it was said.
type EpisodeHit struct {
	// EpisodeID is the episode holding the appearance.
	EpisodeID string
	// Number is the episode number, for speech and ordering.
	Number int
	// Quote is the stored wording as heard.
	Quote string
	// Offset is the rendered word index the mention points at.
	Offset int
}

// NameThread is one recurring name with every episode behind it. Counts
// come from stored rows, so a spoken count repeats these numbers.
type NameThread struct {
	// Key is the normalised name every appearance folds into.
	Key string
	// Display is the most recent stored wording.
	Display string
	// Kind is the provider entity kind behind the thread.
	Kind string
	// Episodes holds every appearance, oldest episode first.
	Episodes []EpisodeHit
	// MentionCount counts the stored mentions behind the thread.
	MentionCount int
	// EpisodeCount counts the distinct episodes behind the thread.
	EpisodeCount int
}

// mentionRow is one stored mention joined to its episode number.
type mentionRow struct {
	id        string
	episodeID string
	number    int
	kind      string
	offset    int
	quote     string
	rowid     int64
}

// loadMentions reads every mention for the owner with its episode number,
// oldest episode first. Grouping and ranking build on this order.
func loadMentions(ctx context.Context, db *sql.DB, ownerID string) ([]mentionRow, error) {
	const query = `SELECT m.id, m.episode_id, e.number, m.kind, m.word_offset, m.quote, m.rowid
		FROM mentions AS m
		JOIN episodes AS e ON e.id = m.episode_id
		WHERE m.owner_id = ?
		ORDER BY e.number ASC, m.rowid ASC`
	rows, err := db.QueryContext(ctx, query, ownerID)
	if err != nil {
		return nil, fmt.Errorf("memory: load mentions: %w", err)
	}
	defer rows.Close()
	var out []mentionRow
	for rows.Next() {
		var row mentionRow
		if err := rows.Scan(&row.id, &row.episodeID, &row.number, &row.kind, &row.offset, &row.quote, &row.rowid); err != nil {
			return nil, fmt.Errorf("memory: load mentions: %w", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("memory: load mentions: %w", err)
	}
	return out, nil
}

// threadGroup accumulates one normalised key while loading.
type threadGroup struct {
	key      string
	display  string
	kind     string
	hits     []EpisodeHit
	episodes map[int]bool
}

// groupMentions folds rows into normalised groups. Blank keys never form
// a thread. The display keeps the most recent wording, so the index shows
// the name as last heard.
func groupMentions(rows []mentionRow, skip func(kind string) bool) []*threadGroup {
	byKey := make(map[string]*threadGroup)
	var order []*threadGroup
	for _, row := range rows {
		if skip(row.kind) {
			continue
		}
		key := NormalizeName(row.quote)
		if key == "" {
			continue
		}
		group, found := byKey[key]
		if !found {
			group = &threadGroup{key: key, kind: row.kind, episodes: make(map[int]bool)}
			byKey[key] = group
			order = append(order, group)
		}
		group.display = row.quote
		group.hits = append(group.hits, EpisodeHit{
			EpisodeID: row.episodeID,
			Number:    row.number,
			Quote:     row.quote,
			Offset:    row.offset,
		})
		group.episodes[row.number] = true
	}
	return order
}

// RecurringNames returns entity threads in at least minEpisodes distinct
// episodes. Key phrases, commitments, and doing mentions never qualify,
// because people and places are the thread. Threads sort by episode span,
// then by mention count, then by name, so the most returned to name
// leads.
func RecurringNames(ctx context.Context, db *sql.DB, ownerID string, minEpisodes int) ([]NameThread, error) {
	if db == nil || ownerID == "" || minEpisodes < 1 {
		return nil, fmt.Errorf("memory: recurring names: %w", ErrInvalid)
	}
	rows, err := loadMentions(ctx, db, ownerID)
	if err != nil {
		return nil, err
	}
	var out []NameThread
	for _, group := range groupMentions(rows, func(kind string) bool {
		return kind == KeyphraseKind || kind == CommitmentKind || kind == DoingKind
	}) {
		if len(group.episodes) < minEpisodes {
			continue
		}
		out = append(out, NameThread{
			Key:          group.key,
			Display:      group.display,
			Kind:         group.kind,
			Episodes:     group.hits,
			MentionCount: len(group.hits),
			EpisodeCount: len(group.episodes),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].EpisodeCount != out[j].EpisodeCount {
			return out[i].EpisodeCount > out[j].EpisodeCount
		}
		if out[i].MentionCount != out[j].MentionCount {
			return out[i].MentionCount > out[j].MentionCount
		}
		return out[i].Key < out[j].Key
	})
	return out, nil
}

// CircledTopic is one key phrase the speaker keeps circling with no close
// on record. Episodes name every appearance, oldest first.
type CircledTopic struct {
	// Key is the normalised phrase every appearance folds into.
	Key string
	// Display is the most recent stored wording.
	Display string
	// Episodes holds every appearance, oldest episode first.
	Episodes []EpisodeHit
	// MentionCount counts the stored mentions behind the topic.
	MentionCount int
	// EpisodeCount counts the distinct episodes behind the topic.
	EpisodeCount int
}

// CircledTopics returns key phrase groups in at least three distinct
// episodes with no close on record. A phrase matching a doing mention or
// a closed commitment drops out, because the speaker already reported an
// outcome for it. The rest sort by episode span, then count, then name.
func CircledTopics(ctx context.Context, db *sql.DB, ownerID string) ([]CircledTopic, error) {
	if db == nil || ownerID == "" {
		return nil, fmt.Errorf("memory: circled topics: %w", ErrInvalid)
	}
	rows, err := loadMentions(ctx, db, ownerID)
	if err != nil {
		return nil, err
	}
	closed := make(map[string]bool)
	for _, row := range rows {
		if row.kind != DoingKind {
			continue
		}
		if key := NormalizeName(row.quote); key != "" {
			closed[key] = true
		}
	}
	resolved, err := resolvedCommitmentKeys(ctx, db, ownerID)
	if err != nil {
		return nil, err
	}
	for key := range resolved {
		closed[key] = true
	}
	var out []CircledTopic
	for _, group := range groupMentions(rows, func(kind string) bool { return kind != KeyphraseKind }) {
		if len(group.episodes) < CircledMinEpisodes {
			continue
		}
		if closed[group.key] {
			continue
		}
		out = append(out, CircledTopic{
			Key:          group.key,
			Display:      group.display,
			Episodes:     group.hits,
			MentionCount: len(group.hits),
			EpisodeCount: len(group.episodes),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].EpisodeCount != out[j].EpisodeCount {
			return out[i].EpisodeCount > out[j].EpisodeCount
		}
		if out[i].MentionCount != out[j].MentionCount {
			return out[i].MentionCount > out[j].MentionCount
		}
		return out[i].Key < out[j].Key
	})
	return out, nil
}

// Keyterm is one recurring name ranked for the live session. Count and
// recency come from stored rows, so the transcription boost repeats the
// names the season actually holds.
type Keyterm struct {
	// Term is the most recent stored wording.
	Term string
	// Count counts the stored mentions behind the term.
	Count int
	// LastEpisode is the newest episode number behind the term.
	LastEpisode int
}

// Keyterms ranks distinct normalised names by mention count, then by
// recency. The most recent wording wins each group. A nonpositive limit
// returns at most MaxKeyterms. Every mention kind counts, because people,
// places, and the project the speaker keeps naming all help the session
// hear them.
func Keyterms(ctx context.Context, db *sql.DB, ownerID string, limit int) ([]Keyterm, error) {
	if db == nil || ownerID == "" {
		return nil, fmt.Errorf("memory: keyterms: %w", ErrInvalid)
	}
	if limit <= 0 {
		limit = MaxKeyterms
	}
	rows, err := loadMentions(ctx, db, ownerID)
	if err != nil {
		return nil, err
	}
	groups := groupMentions(rows, func(string) bool { return false })
	ranked := make([]Keyterm, 0, len(groups))
	for _, group := range groups {
		last := 0
		for _, hit := range group.hits {
			if hit.Number > last {
				last = hit.Number
			}
		}
		ranked = append(ranked, Keyterm{Term: group.display, Count: len(group.hits), LastEpisode: last})
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].Count != ranked[j].Count {
			return ranked[i].Count > ranked[j].Count
		}
		return ranked[i].LastEpisode > ranked[j].LastEpisode
	})
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}
	return ranked, nil
}

// Counts is the spoken arithmetic behind one name. Every number comes
// from stored rows, so the host repeats a query result.
type Counts struct {
	// Mentions counts the stored mentions behind the name.
	Mentions int
	// Episodes counts the distinct episodes behind the name.
	Episodes int
	// FirstEpisode is the oldest episode number behind the name.
	FirstEpisode int
	// LastEpisode is the newest episode number behind the name.
	LastEpisode int
}

// CountMentions returns the query result behind one normalised name. An
// unknown name returns zero counts and no error, because absence is an
// answer the host can speak.
func CountMentions(ctx context.Context, db *sql.DB, ownerID, name string) (Counts, error) {
	if db == nil || ownerID == "" {
		return Counts{}, fmt.Errorf("memory: count mentions: %w", ErrInvalid)
	}
	key := NormalizeName(name)
	if key == "" {
		return Counts{}, fmt.Errorf("memory: count mentions: %w: blank name", ErrInvalid)
	}
	rows, err := loadMentions(ctx, db, ownerID)
	if err != nil {
		return Counts{}, err
	}
	var out Counts
	seen := make(map[int]bool)
	for _, row := range rows {
		if NormalizeName(row.quote) != key {
			continue
		}
		out.Mentions++
		seen[row.number] = true
		if out.FirstEpisode == 0 || row.number < out.FirstEpisode {
			out.FirstEpisode = row.number
		}
		if row.number > out.LastEpisode {
			out.LastEpisode = row.number
		}
	}
	out.Episodes = len(seen)
	return out, nil
}
