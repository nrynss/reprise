// Package host builds the live session config from stored rows.
//
// The only name in the fixed wording is the chosen voice's own, and the
// only digits are the numbered steps of the flow. Every other name,
// episode number, and count the host speaks comes from a diary row. The
// wording around those values is fixed. Nothing here invents speech
// content. The caller reads the config and forwards it to the provider.
package host

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"
)

// ErrInvalid reports an argument Load cannot honour, such as a nil database
// or an empty owner.
var ErrInvalid = errors.New("host: invalid argument")

// Voice is one host voice the guest can pick. ID is the provider's voice id.
// Name is what the host calls itself.
type Voice struct {
	ID   string
	Name string
}

// Voices lists the host voices on offer, in the order the page shows them.
var Voices = []Voice{{ID: "anna", Name: "Anna"}, {ID: "george", Name: "George"}, {ID: "eve", Name: "Eve"}}

// DefaultVoice is the voice id used when the guest picks none.
const DefaultVoice = "anna"

// ErrVoice reports a voice id outside Voices.
var ErrVoice = errors.New("host: unknown voice")

// VoiceByID returns the voice with the given id. The empty string returns
// the Anna entry. Any other id outside Voices returns an error wrapping
// ErrVoice. The match is exact, with no trimming and no case folding.
func VoiceByID(id string) (Voice, error) {
	if id == "" {
		return Voices[0], nil
	}
	for _, voice := range Voices {
		if voice.ID == id {
			return voice, nil
		}
	}
	return Voice{}, fmt.Errorf("host: voice %q: %w", id, ErrVoice)
}

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
	// PriorEpisodes counts the owner's earlier episodes, excluding the
	// recording row the broker has not created yet.
	PriorEpisodes int
	// Voice is the host voice the guest picked. A zero Voice reads as Anna,
	// so inputs built by hand without a voice keep working.
	Voice Voice
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
	// Voice is the provider voice id for the session setup frame.
	Voice string `json:"voice"`
	// CallbackID names the planted callback the greeting cites. It stays
	// empty when the greeting cites no callback, and it never leaves the
	// process over JSON.
	CallbackID string `json:"-"`
}

// opener greets a season with no planted callback. The placeholder takes the
// voice name, so the host introduces itself as whoever the guest picked.
const opener = "Hey, I'm {name}. Welcome to your first episode! What's on your mind today?"

// returningOpener greets an owner who already holds episodes but has no
// planted callback waiting.
const returningOpener = "Hey, welcome back! What's on your mind today?"

// promptBody holds the fixed host voice. The placeholder takes the voice
// name. The example dessert lines are fixed wording, never stored threads.
// The text uses only lower-case episode, so a count of capital Episode
// lines still counts threads. The numbered flow steps hold the only digits.
const promptBody = `You are {name}, the host of a personal podcast. One guest, talking about their own life.

Listen and be empathetic. This is the most important rule.
- Talk the way people talk on a good podcast: natural, a little loose, never a speech.
- Say what you want to say, then hand the turn back. One question per turn.
- If they pause mid-thought, wait. Don't finish their sentence.

Energy:
- Open bright. You're glad they showed up, and it shows.
- Then follow them. Match their pace and mood as the conversation goes.
- If they're excited, get excited with them. If they slow down or go quiet, you do too.
- Never stay louder than the guest. Their energy sets the room.

React like a friend, not a fan. No gushing, no praising their story.
Don't recap what they just said. They were there.

You're a host, not a therapist. Don't name their feelings for them,
don't counsel, don't fix. Ask about what happened, not how it made them feel.
If something's heavy, a short, plain reply is enough.

Bad: "Oh, that sounds absolutely magical! It must have meant a lot to share that with the kids."
Good: "Wait, the dessert looked like a snitch? Okay, I need to know. Did anyone actually eat it, or did it just sit there looking pretty?"

You CAN:
- Bring up something from an earlier episode, if one is listed at the end.
- Ask about a detail they just mentioned.
- Let a topic go when they move on.

You CANNOT:
- Invent an episode, a name, a date or a count. Only the episodes listed at the end happened. If none are listed, this is their first.
- Give advice unless they ask. If they do, keep it brief, then hand it back.
- Talk about your own life. If asked: "Not my episode. So, what happened next?"

Flow:
1. Open with the greeting below, once.
2. Follow what they bring. Their topic beats your callback.
3. Pick a concrete detail and ask about it: a person, a place, what happened next.
4. When they sign off, let them go warmly. No summary.

Say numbers out loud the way people do: "episode twelve", never the digits.
Casual words are fine: "yeah", "oh wow", "huh", "right", "no way", "fair". Use contractions.

Vibe: a good friend with a microphone. Curious, warm, never performing.

Greeting:`

// Build renders a Config from stored rows. The greeting cites the planted
// callback with its episode, or falls back to the opener that matches the
// episode count. The prompt quotes up to three recent threads and binds the
// model to them. A zero voice reads as Anna.
func Build(in Input) Config {
	voice := in.Voice
	if voice.Name == "" {
		voice = Voices[0]
	}
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
	greeting := strings.ReplaceAll(opener, "{name}", voice.Name)
	if in.PriorEpisodes > 0 {
		greeting = returningOpener
	}
	callbackID := ""
	if in.Callback != nil {
		quote := cleanQuote(in.Callback.Mention.Quote)
		episode := in.Callback.Mention.EpisodeNumber
		if quote != "" && episode >= 1 {
			greeting = fmt.Sprintf("Hey, welcome back! Last time in episode %d you mentioned %q, so tell me how that went.",
				episode, quote)
			callbackID = in.Callback.ID
		}
	}
	return Config{
		Greeting:     greeting,
		SystemPrompt: systemPrompt(voice.Name, greeting, threads),
		Keyterms:     terms,
		Voice:        voice.ID,
		CallbackID:   callbackID,
	}
}

// systemPrompt renders the host voice, the interview style, and the stored
// threads. The only name in the fixed wording is the voice's own, and the
// only digits are the numbered steps of the flow. The closing rule binds
// the model to the listed rows, so the wording only phrases them.
func systemPrompt(name, greeting string, threads []Thread) string {
	var out strings.Builder
	out.WriteString(strings.ReplaceAll(promptBody, "{name}", name))
	out.WriteString("\n")
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
// then builds the session config for the default voice. It reads only that
// owner's rows. A season with no planted callback gets the opener that
// matches its episode count, which holds only earlier episodes because the
// new row does not exist yet.
func Load(ctx context.Context, db *sql.DB, ownerID string) (Config, error) {
	return LoadVoice(ctx, db, ownerID, DefaultVoice)
}

// LoadVoice reads the owner's planted callback, recent threads, and
// keyterms, then builds the session config for the given voice id. It
// returns the voice error before any query. It reads only that owner's
// rows. A season with no planted callback gets the opener that matches its
// episode count, which holds only earlier episodes because the new row does
// not exist yet. Every mention and callback it uses holds the guest's own
// words.
func LoadVoice(ctx context.Context, db *sql.DB, ownerID, voiceID string) (Config, error) {
	voice, err := VoiceByID(voiceID)
	if err != nil {
		return Config{}, err
	}
	if db == nil || ownerID == "" {
		return Config{}, fmt.Errorf("host: load: %w", ErrInvalid)
	}
	guest, err := guestText(ctx, db, ownerID)
	if err != nil {
		return Config{}, err
	}
	marked, err := labelled(ctx, db, ownerID)
	if err != nil {
		return Config{}, err
	}
	callback, err := unusedCallback(ctx, db, ownerID, guest, marked)
	if err != nil {
		return Config{}, err
	}
	recent, err := recentMentions(ctx, db, ownerID, guest, marked)
	if err != nil {
		return Config{}, err
	}
	prior, err := priorEpisodes(ctx, db, ownerID)
	if err != nil {
		return Config{}, err
	}
	return Build(Input{
		Callback:      callback,
		Threads:       threadsFrom(recent),
		Keyterms:      keytermsFrom(recent),
		PriorEpisodes: prior,
		Voice:         voice,
	}), nil
}

// MaxThreads caps the recent threads quoted in the system prompt.
const MaxThreads = 3

// MaxKeyterms caps the names forwarded as keyterms.
const MaxKeyterms = 100

// priorEpisodes counts the owner's episodes outside the open recording
// row. The broker loads the config before it creates the new episode, so
// the count holds only earlier episodes.
func priorEpisodes(ctx context.Context, db *sql.DB, ownerID string) (int, error) {
	const query = `SELECT COUNT(*) FROM episodes WHERE owner_id = ? AND state != 'recording'`
	var count int
	if err := db.QueryRowContext(ctx, query, ownerID).Scan(&count); err != nil {
		return 0, fmt.Errorf("host: load episode count: %w", err)
	}
	return count, nil
}

// unusedCallback returns the oldest unused planted callback quoting the
// guest's own words, joined to its source mention and the number of the
// episode it came from. It returns nil when the season holds no unused
// callback from the guest. It never updates or deletes a row, so the caller
// claims the callback it cites.
func unusedCallback(ctx context.Context, db *sql.DB, ownerID string, guest map[string]string, marked map[string]bool) (*Callback, error) {
	const query = `SELECT c.id, m.id, m.kind, m.quote, e.number, m.episode_id
		FROM callbacks AS c
		JOIN mentions AS m ON m.id = c.mention_id AND m.owner_id = c.owner_id
		JOIN episodes AS e ON e.id = m.episode_id
		WHERE c.owner_id = ? AND c.used = 0
		ORDER BY c.rowid ASC`
	rows, err := db.QueryContext(ctx, query, ownerID)
	if err != nil {
		return nil, fmt.Errorf("host: load callback: %w", err)
	}
	defer rows.Close() // the rows drain below, so close reports nothing new
	for rows.Next() {
		var id, mentionID, kind, quote, episodeID string
		var number int
		if err := rows.Scan(&id, &mentionID, &kind, &quote, &number, &episodeID); err != nil {
			return nil, fmt.Errorf("host: load callback: %w", err)
		}
		if marked[episodeID] {
			normal := normalise(quote)
			words, found := guest[episodeID]
			if normal == "" || !found || !strings.Contains(words, normal) {
				continue
			}
		}
		return &Callback{
			ID: id,
			Mention: Mention{
				ID:            mentionID,
				EpisodeNumber: number,
				Kind:          kind,
				Quote:         quote,
			},
		}, nil
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("host: load callback: %w", err)
	}
	return nil, nil
}

// recentMentions returns every stored excerpt quoting the guest, most
// recent episode first. Episodes without speaker labels keep every
// mention. Callers dedupe and rank from this order.
func recentMentions(ctx context.Context, db *sql.DB, ownerID string, guest map[string]string, marked map[string]bool) ([]Thread, error) {
	const query = `SELECT e.number, m.kind, m.quote, m.episode_id
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
		var episodeID string
		if err := rows.Scan(&thread.EpisodeNumber, &thread.Kind, &thread.Quote, &episodeID); err != nil {
			return nil, fmt.Errorf("host: load mentions: %w", err)
		}
		if marked[episodeID] {
			normal := normalise(thread.Quote)
			words, found := guest[episodeID]
			if normal == "" || !found || !strings.Contains(words, normal) {
				continue
			}
		}
		recent = append(recent, thread)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("host: load mentions: %w", err)
	}
	return recent, nil
}

// guestText joins every guest word per episode and normalises the result.
// The literal source and speaker match the stored values the transcript
// package writes, without importing it.
func guestText(ctx context.Context, db *sql.DB, ownerID string) (map[string]string, error) {
	const query = `SELECT episode_id, text FROM words
		WHERE owner_id = ? AND source = 'edit' AND speaker = 'user'
		ORDER BY episode_id, rowid`
	rows, err := db.QueryContext(ctx, query, ownerID)
	if err != nil {
		return nil, fmt.Errorf("host: load guest words: %w", err)
	}
	defer rows.Close() // the rows drain below, so close reports nothing new
	joined := make(map[string]string)
	for rows.Next() {
		var episodeID, text string
		if err := rows.Scan(&episodeID, &text); err != nil {
			return nil, fmt.Errorf("host: load guest words: %w", err)
		}
		if prior := joined[episodeID]; prior == "" {
			joined[episodeID] = text
		} else {
			joined[episodeID] = prior + " " + text
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("host: load guest words: %w", err)
	}
	out := make(map[string]string, len(joined))
	for episodeID, text := range joined {
		out[episodeID] = normalise(text)
	}
	return out, nil
}

// labelled reports every episode holding at least one stored edit word, of
// any speaker. The literal source matches the stored value the transcript
// package writes, without importing it.
func labelled(ctx context.Context, db *sql.DB, ownerID string) (map[string]bool, error) {
	const query = `SELECT DISTINCT episode_id FROM words WHERE owner_id = ? AND source = 'edit'`
	rows, err := db.QueryContext(ctx, query, ownerID)
	if err != nil {
		return nil, fmt.Errorf("host: load labels: %w", err)
	}
	defer rows.Close() // the rows drain below, so close reports nothing new
	marked := make(map[string]bool)
	for rows.Next() {
		var episodeID string
		if err := rows.Scan(&episodeID); err != nil {
			return nil, fmt.Errorf("host: load labels: %w", err)
		}
		marked[episodeID] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("host: load labels: %w", err)
	}
	return marked, nil
}

// normalise lower-cases s, turns every rune that is not a letter or a digit
// into a space, then joins the fields with single spaces.
func normalise(s string) string {
	fields := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	return strings.Join(fields, " ")
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
