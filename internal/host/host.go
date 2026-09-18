// Package host builds the live session config from stored rows.
//
// Every name, episode number, and count the host speaks comes from a diary
// row. The wording around those values is fixed. Nothing here invents speech
// content. The caller reads the config and forwards it to the provider.
package host

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// ErrInvalid reports an argument Load cannot honour, such as a nil database
// or an empty owner.
var ErrInvalid = errors.New("host: invalid argument")

// MaxThreads caps the recent threads quoted in the system prompt.
const MaxThreads = 3

// MaxKeyterms caps the names forwarded as keyterms.
const MaxKeyterms = 100

// Mention is one stored mention with the number of the episode it came from.
type Mention struct {
	// ID is the mention row id.
	ID string
	// EpisodeNumber is the number of the episode holding the mention.
	EpisodeNumber int
	// Kind is the mention kind, such as person, place, topic, or commitment.
	Kind string
	// Quote is the stored verbatim excerpt.
	Quote string
}

// Callback is a planted callback joined to its source mention.
type Callback struct {
	// ID is the callback row id.
	ID string
	// Mention is the source mention the host calls back to.
	Mention Mention
}

// Thread is one recent thread quoted in the system prompt.
type Thread struct {
	// EpisodeNumber is the number of the episode holding the quote.
	EpisodeNumber int
	// Kind is the mention kind behind the thread.
	Kind string
	// Quote is the stored verbatim excerpt.
	Quote string
}

// Input is everything Build needs. Load reads it from the database, and
// tests build it by hand.
type Input struct {
	// Callback is the planted callback, or nil when the season has none.
	Callback *Callback
	// Threads holds the recent threads, most recent first.
	Threads []Thread
	// Keyterms holds the recurring names, most frequent first.
	Keyterms []string
}

// Config is the session config the broker answers with. The JSON names match
// the provider session update, so the socket layer forwards them unchanged.
type Config struct {
	// Greeting is the one sentence the host speaks first.
	Greeting string `json:"greeting"`
	// SystemPrompt carries the host voice and the stored threads.
	SystemPrompt string `json:"system_prompt"`
	// Keyterms holds up to one hundred recurring names.
	Keyterms []string `json:"keyterms"`
}

// opener greets a season with no planted callback.
const opener = "Welcome to your first episode, tell me what is on your mind today."

// Build renders a Config from stored rows. The greeting cites the planted
// callback with its episode, or falls back to the first-episode opener. The
// prompt quotes up to three recent threads and binds the model to them.
func Build(in Input) Config {
	threads := in.Threads
	if len(threads) > MaxThreads {
		threads = threads[:MaxThreads]
	}
	terms := make([]string, 0, min(len(in.Keyterms), MaxKeyterms))
	for _, term := range in.Keyterms {
		if len(terms) >= MaxKeyterms {
			break
		}
		if strings.TrimSpace(term) == "" {
			continue
		}
		terms = append(terms, term)
	}
	greeting := opener
	if in.Callback != nil {
		quote := cleanQuote(in.Callback.Mention.Quote)
		episode := in.Callback.Mention.EpisodeNumber
		if quote != "" && episode >= 1 {
			greeting = fmt.Sprintf("Last time in episode %d you mentioned %q, so tell me how that went.",
				episode, quote)
		}
	}
	return Config{
		Greeting:     greeting,
		SystemPrompt: systemPrompt(greeting, threads),
		Keyterms:     terms,
	}
}

// systemPrompt renders the host voice, the interview style, and the stored
// threads. The fixed wording holds no names and no digits. The closing rule
// binds the model to the listed rows, so the wording only phrases them.
func systemPrompt(greeting string, threads []Thread) string {
	var out strings.Builder
	out.WriteString("You are the host of a personal podcast. ")
	out.WriteString("You are warm, curious, and direct. ")
	out.WriteString("You interview one guest about their life. ")
	out.WriteString("You interrupt to follow up when something matters. ")
	out.WriteString("You ask a single question at a time, then you listen. ")
	out.WriteString("You open with the greeting below exactly once, before anything else:\n")
	out.WriteString(greeting)
	if len(threads) > 0 {
		out.WriteString("\n\nThings the guest said before. ")
		out.WriteString("Mention these only as written here, with the episode given:\n")
		for _, thread := range threads {
			fmt.Fprintf(&out, "Episode %d (%s): %q\n",
				thread.EpisodeNumber, thread.Kind, cleanQuote(thread.Quote))
		}
	}
	out.WriteString("\nUse only the names, episodes, and counts listed above. ")
	out.WriteString("Never invent a name, an episode, or a count. ")
	out.WriteString("If the guest names someone new, you may repeat it back. ")
	out.WriteString("If a thread is missing here, it never happened.")
	return out.String()
}

// Load reads the owner's planted callback, recent threads, and keyterms,
// then builds the session config. It reads only that owner's rows. A season
// with no planted callback gets the first-episode opener.
func Load(ctx context.Context, db *sql.DB, ownerID string) (Config, error) {
	if db == nil || ownerID == "" {
		return Config{}, fmt.Errorf("host: load: %w", ErrInvalid)
	}
	callback, err := unusedCallback(ctx, db, ownerID)
	if err != nil {
		return Config{}, err
	}
	recent, err := recentMentions(ctx, db, ownerID)
	if err != nil {
		return Config{}, err
	}
	return Build(Input{
		Callback: callback,
		Threads:  threadsFrom(recent),
		Keyterms: keytermsFrom(recent),
	}), nil
}

// unusedCallback returns the oldest unused planted callback for the owner,
// joined to its source mention and the number of the episode it came from.
// It returns nil when the season holds no unused callback.
func unusedCallback(ctx context.Context, db *sql.DB, ownerID string) (*Callback, error) {
	const query = `SELECT c.id, m.id, m.kind, m.quote, e.number
		FROM callbacks AS c
		JOIN mentions AS m ON m.id = c.mention_id AND m.owner_id = c.owner_id
		JOIN episodes AS e ON e.id = m.episode_id
		WHERE c.owner_id = ? AND c.used = 0
		ORDER BY c.rowid ASC
		LIMIT 1`
	var callback Callback
	err := db.QueryRowContext(ctx, query, ownerID).Scan(
		&callback.ID,
		&callback.Mention.ID,
		&callback.Mention.Kind,
		&callback.Mention.Quote,
		&callback.Mention.EpisodeNumber,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("host: load callback: %w", err)
	}
	return &callback, nil
}

// recentMentions returns every stored excerpt for the owner, most recent
// episode first. Callers dedupe and rank from this order.
func recentMentions(ctx context.Context, db *sql.DB, ownerID string) ([]Thread, error) {
	const query = `SELECT e.number, m.kind, m.quote
		FROM mentions AS m
		JOIN episodes AS e ON e.id = m.episode_id
		WHERE m.owner_id = ?
		ORDER BY e.number DESC, m.rowid DESC`
	rows, err := db.QueryContext(ctx, query, ownerID)
	if err != nil {
		return nil, fmt.Errorf("host: load mentions: %w", err)
	}
	defer rows.Close() // the rows drain below, so close reports nothing new
	var recent []Thread
	for rows.Next() {
		var thread Thread
		if err := rows.Scan(&thread.EpisodeNumber, &thread.Kind, &thread.Quote); err != nil {
			return nil, fmt.Errorf("host: load mentions: %w", err)
		}
		recent = append(recent, thread)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("host: load mentions: %w", err)
	}
	return recent, nil
}

// threadsFrom keeps the first three distinct excerpts in recency order.
// Blank excerpts never reach the prompt.
func threadsFrom(recent []Thread) []Thread {
	var threads []Thread
	seen := make(map[string]bool)
	for _, thread := range recent {
		if len(threads) >= MaxThreads {
			break
		}
		quote := cleanQuote(thread.Quote)
		if quote == "" {
			continue
		}
		key := strings.ToLower(quote)
		if seen[key] {
			continue
		}
		seen[key] = true
		threads = append(threads, Thread{
			EpisodeNumber: thread.EpisodeNumber,
			Kind:          thread.Kind,
			Quote:         quote,
		})
	}
	return threads
}

// keyCount ranks one distinct excerpt by frequency and recency.
type keyCount struct {
	// surface is the most recent stored wording.
	surface string
	// count is the number of stored excerpts behind it.
	count int
	// first is the index of its most recent occurrence.
	first int
}

// keytermsFrom ranks distinct excerpts by count, then by recency. The most
// recent wording wins when one name arrives in several shapes.
func keytermsFrom(recent []Thread) []string {
	ranked := make([]keyCount, 0, len(recent))
	byKey := make(map[string]int)
	for i, thread := range recent {
		quote := cleanQuote(thread.Quote)
		if quote == "" {
			continue
		}
		key := strings.ToLower(quote)
		at, found := byKey[key]
		if !found {
			byKey[key] = len(ranked)
			ranked = append(ranked, keyCount{surface: quote, count: 1, first: i})
			continue
		}
		ranked[at].count++
	}
	slices.SortStableFunc(ranked, func(a, b keyCount) int {
		if a.count != b.count {
			return b.count - a.count
		}
		return a.first - b.first
	})
	terms := make([]string, 0, min(len(ranked), MaxKeyterms))
	for _, entry := range ranked {
		if len(terms) >= MaxKeyterms {
			break
		}
		terms = append(terms, entry.surface)
	}
	return terms
}

// cleanQuote collapses a stored excerpt to one line for speech. It trims
// surrounding quotes and swaps inner doubles for singles, so the greeting
// keeps one clear quoted span.
func cleanQuote(quote string) string {
	quote = strings.Join(strings.Fields(quote), " ")
	quote = strings.Trim(quote, `"' `)
	return strings.ReplaceAll(quote, `"`, `'`)
}
