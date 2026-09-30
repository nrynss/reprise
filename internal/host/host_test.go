package host_test

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nrynss/keel/sqlite"
	"github.com/nrynss/reprise/internal/host"
	"github.com/nrynss/reprise/internal/store"
)

// openSeason migrates a fresh diary file and returns its writer. Records go
// to a discarding logger so a passing test stays quiet.
func openSeason(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sqlite.Open(t.Context(), sqlite.Config{
		Path:   filepath.Join(t.TempDir(), "season.db"),
		Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() }) // the handle is discarded here, so a close failure cannot fail the test
	if _, err := store.Open(t.Context(), db); err != nil {
		t.Fatalf("open store: %v", err)
	}
	return db.Writer()
}

// mustExec runs a statement and fails the test on error.
func mustExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

// addOwner writes one user row.
func addOwner(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	mustExec(t, db, "INSERT INTO users (id, kind, created_at, last_seen_at) VALUES (?, 'guest', 1, 2)", id)
}

// addEpisode writes one episode row with its number.
func addEpisode(t *testing.T, db *sql.DB, id, owner string, number int, state string) {
	t.Helper()
	mustExec(t, db, "INSERT INTO episodes (id, owner_id, number, title, state, visibility, share_token, seeded) VALUES (?, ?, ?, ?, ?, 'private', ?, 1)",
		id, owner, number, fmt.Sprintf("Episode %d", number), state, "share-"+id)
}

// addMention writes one mention row for the owner.
func addMention(t *testing.T, db *sql.DB, id, owner, episode, kind string, offset int, quote string) {
	t.Helper()
	mustExec(t, db, "INSERT INTO mentions (id, owner_id, episode_id, kind, word_offset, quote) VALUES (?, ?, ?, ?, ?, ?)",
		id, owner, episode, kind, offset, quote)
}

// seedSeason writes four episodes of history plus an upcoming fifth. The
// callback points at the dreaded talk from episode one. Every id carries the
// owner, so two owners seed side by side.
func seedSeason(t *testing.T, db *sql.DB, owner string) {
	t.Helper()
	addOwner(t, db, owner)
	prefix := owner + "-"
	addEpisode(t, db, prefix+"ep1", owner, 1, "ready")
	addEpisode(t, db, prefix+"ep2", owner, 2, "ready")
	addEpisode(t, db, prefix+"ep3", owner, 3, "ready")
	addEpisode(t, db, prefix+"ep4", owner, 4, "ready")
	addEpisode(t, db, prefix+"ep5", owner, 5, "recording")
	addMention(t, db, prefix+"m1", owner, prefix+"ep1", "person", 12, "Maya")
	addMention(t, db, prefix+"m2", owner, prefix+"ep1", "topic", 40, "the talk I keep dreading with my sister")
	addMention(t, db, prefix+"m3", owner, prefix+"ep1", "place", 55, "Lisbon")
	addMention(t, db, prefix+"m4", owner, prefix+"ep2", "person", 8, "Maya")
	addMention(t, db, prefix+"m5", owner, prefix+"ep2", "topic", 20, "the allotment")
	addMention(t, db, prefix+"m6", owner, prefix+"ep3", "person", 5, "Maya")
	addMention(t, db, prefix+"m7", owner, prefix+"ep3", "place", 30, "Lisbon")
	addMention(t, db, prefix+"m8", owner, prefix+"ep3", "commitment", 44, "I will call Maya back")
	addMention(t, db, prefix+"m9", owner, prefix+"ep4", "topic", 10, "the allotment")
	addMention(t, db, prefix+"m10", owner, prefix+"ep4", "person", 25, "Priya")
	mustExec(t, db, "INSERT INTO callbacks (id, owner_id, episode_id, mention_id, used) VALUES (?, ?, ?, ?, 0)",
		prefix+"cb1", owner, prefix+"ep5", prefix+"m2")
}

// storedQuotes returns every mention quote for the owner.
func storedQuotes(t *testing.T, db *sql.DB, owner string) []string {
	t.Helper()
	rows, err := db.QueryContext(t.Context(), "SELECT quote FROM mentions WHERE owner_id = ?", owner)
	if err != nil {
		t.Fatalf("query quotes: %v", err)
	}
	defer rows.Close() // the rows drain below, so close reports nothing new
	var quotes []string
	for rows.Next() {
		var quote string
		if err := rows.Scan(&quote); err != nil {
			t.Fatalf("scan quote: %v", err)
		}
		quotes = append(quotes, quote)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read quotes: %v", err)
	}
	return quotes
}

// TestGoldenConfigFromFixtureSeason loads the config from four episodes of
// history and pins the greeting, the keyterms, and the prompt threads.
func TestGoldenConfigFromFixtureSeason(t *testing.T) {
	t.Parallel()
	db := openSeason(t)
	seedSeason(t, db, "owner-a")
	config, err := host.Load(t.Context(), db, "owner-a")
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	wantGreeting := `Hey, welcome back! Last time in episode 1 you mentioned "the talk I keep dreading with my sister", so tell me how that went.`
	if config.Greeting != wantGreeting {
		t.Fatalf("greeting = %q, want %q", config.Greeting, wantGreeting)
	}
	wantTerms := []string{"Maya", "the allotment", "Lisbon", "Priya", "I will call Maya back", "the talk I keep dreading with my sister"}
	if !slices.Equal(config.Keyterms, wantTerms) {
		t.Fatalf("keyterms = %q, want %q", config.Keyterms, wantTerms)
	}
	for _, want := range []string{
		config.Greeting,
		`Episode 4 (topic): "the allotment"`,
		`Episode 4 (person): "Priya"`,
		`Episode 3 (commitment): "I will call Maya back"`,
		"Use only the names, episodes, and counts listed above.",
		"One question per turn.",
		"If they pause mid-thought, wait.",
	} {
		if !strings.Contains(config.SystemPrompt, want) {
			t.Fatalf("system prompt misses %q:\n%s", want, config.SystemPrompt)
		}
	}
	if threads := strings.Count(config.SystemPrompt, "Episode "); threads != 3 {
		t.Fatalf("system prompt quotes %d threads, want 3", threads)
	}
}

// TestGreetingProvenance checks every quoted span and every digit run in the
// greeting against the stored rows. A name or count the host speaks must
// exist in a row.
func TestGreetingProvenance(t *testing.T) {
	t.Parallel()
	db := openSeason(t)
	seedSeason(t, db, "owner-a")
	config, err := host.Load(t.Context(), db, "owner-a")
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	quotes := storedQuotes(t, db, "owner-a")
	numbers := map[string]bool{"1": true, "2": true, "3": true, "4": true, "5": true}
	rest := config.Greeting
	for {
		open := strings.Index(rest, `"`)
		if open < 0 {
			break
		}
		rest = rest[open+1:]
		close := strings.Index(rest, `"`)
		if close < 0 {
			t.Fatalf("greeting holds an unterminated quote: %q", config.Greeting)
		}
		if span := rest[:close]; !slices.Contains(quotes, span) {
			t.Fatalf("greeting quotes %q, which no stored row holds", span)
		}
		rest = rest[close+1:]
	}
	var digits strings.Builder
	for _, r := range config.Greeting {
		if r >= '0' && r <= '9' {
			digits.WriteRune(r)
		} else if digits.Len() > 0 {
			if !numbers[digits.String()] {
				t.Fatalf("greeting counts %q, which no stored row holds", digits.String())
			}
			digits.Reset()
		}
	}
	if digits.Len() > 0 && !numbers[digits.String()] {
		t.Fatalf("greeting counts %q, which no stored row holds", digits.String())
	}
	if n := strings.Count(config.Greeting, "."); n != 1 {
		t.Fatalf("greeting holds %d sentence ends, want one sentence", n)
	}
}

// TestFirstEpisodeOpener loads a season with no callback and pins the warm
// opener with empty keyterms.
func TestFirstEpisodeOpener(t *testing.T) {
	t.Parallel()
	db := openSeason(t)
	addOwner(t, db, "owner-new")
	config, err := host.Load(t.Context(), db, "owner-new")
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	want := "Hey, I'm Anna. Welcome to your first episode! What's on your mind today?"
	if config.Greeting != want {
		t.Fatalf("greeting = %q, want %q", config.Greeting, want)
	}
	if len(config.Keyterms) != 0 {
		t.Fatalf("keyterms = %q, want none for a new season", config.Keyterms)
	}
	if strings.Contains(config.SystemPrompt, "Episode ") {
		t.Fatalf("system prompt quotes threads for a season with none:\n%s", config.SystemPrompt)
	}
	if !strings.Contains(config.SystemPrompt, config.Greeting) {
		t.Fatalf("system prompt misses the greeting:\n%s", config.SystemPrompt)
	}
}

// TestUsedCallbackIgnoredAndOwnerIsolated plants a used callback for one
// owner and an unused one for another. Each owner hears only their own rows.
func TestUsedCallbackIgnoredAndOwnerIsolated(t *testing.T) {
	t.Parallel()
	db := openSeason(t)
	seedSeason(t, db, "owner-a")
	mustExec(t, db, "UPDATE callbacks SET used = 1 WHERE id = 'owner-a-cb1'")
	plain, err := host.Load(t.Context(), db, "owner-a")
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if plain.Greeting != "Hey, welcome back! What's on your mind today?" {
		t.Fatalf("greeting = %q, want the returning opener once the callback is used", plain.Greeting)
	}
	seedSeason(t, db, "owner-b")
	other, err := host.Load(t.Context(), db, "owner-b")
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if !strings.Contains(other.Greeting, "the talk I keep dreading with my sister") {
		t.Fatalf("greeting = %q, want owner-b to hear their own callback", other.Greeting)
	}
	if strings.Contains(other.SystemPrompt, "owner-a") {
		t.Fatalf("system prompt leaks across owners:\n%s", other.SystemPrompt)
	}
}

// TestKeytermsCapAtOneHundred seeds more than one hundred distinct names
// and checks the config keeps the most frequent one hundred.
func TestKeytermsCapAtOneHundred(t *testing.T) {
	t.Parallel()
	db := openSeason(t)
	addOwner(t, db, "owner-many")
	addEpisode(t, db, "ep1", "owner-many", 1, "ready")
	for i := range 120 {
		addMention(t, db, fmt.Sprintf("cap-%d", i), "owner-many", "ep1", "person", i, fmt.Sprintf("Guest %d", i))
	}
	for i := range 5 {
		addMention(t, db, fmt.Sprintf("top-%d", i), "owner-many", "ep1", "person", 200+i, "Maya")
	}
	config, err := host.Load(t.Context(), db, "owner-many")
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if len(config.Keyterms) != host.MaxKeyterms {
		t.Fatalf("keyterms holds %d names, want %d", len(config.Keyterms), host.MaxKeyterms)
	}
	if config.Keyterms[0] != "Maya" {
		t.Fatalf("keyterms[0] = %q, want the most frequent name first", config.Keyterms[0])
	}
}

// TestKeytermsDedupeAcrossShapes writes one name in two casings and checks a
// single keyterm survives with the most recent wording.
func TestKeytermsDedupeAcrossShapes(t *testing.T) {
	t.Parallel()
	db := openSeason(t)
	addOwner(t, db, "owner-case")
	addEpisode(t, db, "ep1", "owner-case", 1, "ready")
	addMention(t, db, "c1", "owner-case", "ep1", "person", 1, "maya")
	addMention(t, db, "c2", "owner-case", "ep1", "person", 2, "Maya")
	config, err := host.Load(t.Context(), db, "owner-case")
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if !slices.Equal(config.Keyterms, []string{"Maya"}) {
		t.Fatalf("keyterms = %q, want one entry with recent wording", config.Keyterms)
	}
}

// TestLoadRejectsBadInput checks the sentinel on a nil database and an
// empty owner, with an empty config beside the error.
func TestLoadRejectsBadInput(t *testing.T) {
	t.Parallel()
	db := openSeason(t)
	config, err := host.Load(t.Context(), nil, "owner-a")
	if !errors.Is(err, host.ErrInvalid) {
		t.Fatalf("load nil database error = %v, want ErrInvalid", err)
	}
	if config.Greeting != "" || config.SystemPrompt != "" || len(config.Keyterms) != 0 {
		t.Fatalf("load nil database config = %+v, want empty", config)
	}
	config, err = host.Load(t.Context(), db, "")
	if !errors.Is(err, host.ErrInvalid) {
		t.Fatalf("load empty owner error = %v, want ErrInvalid", err)
	}
	if config.Greeting != "" || config.SystemPrompt != "" || len(config.Keyterms) != 0 {
		t.Fatalf("load empty owner config = %+v, want empty", config)
	}
}

// TestOpenerFollowsPriorEpisodes checks the greeting without a callback:
// a first season hears the first-episode opener, while a returning owner
// hears the welcome-back opener. A cited callback also reports its id.
func TestOpenerFollowsPriorEpisodes(t *testing.T) {
	t.Parallel()
	first := host.Build(host.Input{PriorEpisodes: 0})
	if want := "Hey, I'm Anna. Welcome to your first episode! What's on your mind today?"; first.Greeting != want {
		t.Fatalf("greeting = %q, want %q", first.Greeting, want)
	}
	if first.CallbackID != "" {
		t.Fatalf("callback id = %q, want empty with no callback", first.CallbackID)
	}
	returning := host.Build(host.Input{PriorEpisodes: 2})
	if want := "Hey, welcome back! What's on your mind today?"; returning.Greeting != want {
		t.Fatalf("greeting = %q, want %q", returning.Greeting, want)
	}
	if returning.CallbackID != "" {
		t.Fatalf("callback id = %q, want empty with no callback", returning.CallbackID)
	}
	cited := host.Build(host.Input{
		PriorEpisodes: 2,
		Callback: &host.Callback{
			ID: "cb-1",
			Mention: host.Mention{
				ID:            "m-1",
				EpisodeNumber: 5,
				Kind:          "person",
				Quote:         "the talk I keep dreading with my sister",
			},
		},
	})
	if !strings.Contains(cited.Greeting, "the talk I keep dreading with my sister") {
		t.Fatalf("greeting = %q, want it to cite the callback quote", cited.Greeting)
	}
	if cited.CallbackID != "cb-1" {
		t.Fatalf("callback id = %q, want cb-1", cited.CallbackID)
	}
}

// TestReturningOpenerLoadsFromStoredEpisodes seeds a returning owner with
// no unused callback and checks Load greets them back, proving the count
// query excludes the open recording row.
func TestReturningOpenerLoadsFromStoredEpisodes(t *testing.T) {
	t.Parallel()
	db := openSeason(t)
	addOwner(t, db, "owner-back")
	addEpisode(t, db, "back-ep1", "owner-back", 1, "ready")
	addEpisode(t, db, "back-ep2", "owner-back", 2, "ready")
	addEpisode(t, db, "back-ep3", "owner-back", 3, "recording")
	config, err := host.Load(t.Context(), db, "owner-back")
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if want := "Hey, welcome back! What's on your mind today?"; config.Greeting != want {
		t.Fatalf("greeting = %q, want %q", config.Greeting, want)
	}
	if config.CallbackID != "" {
		t.Fatalf("callback id = %q, want empty with no callback", config.CallbackID)
	}
}

// TestVoiceByID checks the empty id gives Anna, each known id gives its
// voice, and unknown ids wrap ErrVoice without trimming or case folding.
func TestVoiceByID(t *testing.T) {
	t.Parallel()
	anna, err := host.VoiceByID("")
	if err != nil {
		t.Fatalf("voice by empty id error = %v, want nil", err)
	}
	if anna.ID != "anna" || anna.Name != "Anna" {
		t.Fatalf("voice by empty id = %+v, want the Anna entry", anna)
	}
	for _, want := range []host.Voice{{ID: "anna", Name: "Anna"}, {ID: "george", Name: "George"}, {ID: "eve", Name: "Eve"}} {
		got, err := host.VoiceByID(want.ID)
		if err != nil {
			t.Fatalf("voice by %q error = %v, want nil", want.ID, err)
		}
		if got != want {
			t.Fatalf("voice by %q = %+v, want %+v", want.ID, got, want)
		}
	}
	for _, bad := range []string{"bob", "GEORGE", " eve"} {
		if _, err := host.VoiceByID(bad); !errors.Is(err, host.ErrVoice) {
			t.Fatalf("voice by %q error = %v, want ErrVoice", bad, err)
		}
	}
}

// TestLoadVoiceRejectsUnknownVoice loads with a voice outside the offer and
// checks the voice error arrives before any query runs.
func TestLoadVoiceRejectsUnknownVoice(t *testing.T) {
	t.Parallel()
	db := openSeason(t)
	addOwner(t, db, "owner-voice")
	if _, err := host.LoadVoice(t.Context(), db, "owner-voice", "bob"); !errors.Is(err, host.ErrVoice) {
		t.Fatalf("load voice error = %v, want ErrVoice", err)
	}
	if _, err := host.LoadVoice(t.Context(), nil, "", "bob"); !errors.Is(err, host.ErrVoice) {
		t.Fatalf("load voice on bad input error = %v, want the voice error first", err)
	}
}

// TestBuildVoices builds with each offered voice and checks the prompt
// introduces that voice while the config carries its id.
func TestBuildVoices(t *testing.T) {
	t.Parallel()
	for _, want := range []struct {
		id   string
		name string
	}{{"anna", "Anna"}, {"george", "George"}, {"eve", "Eve"}} {
		voice, err := host.VoiceByID(want.id)
		if err != nil {
			t.Fatalf("voice by %q error = %v, want nil", want.id, err)
		}
		config := host.Build(host.Input{Voice: voice})
		if prefix := "You are " + want.name + ", the host"; !strings.HasPrefix(config.SystemPrompt, prefix) {
			t.Fatalf("system prompt for %q misses prefix %q", want.id, prefix)
		}
		if config.Voice != want.id {
			t.Fatalf("config voice = %q, want %q", config.Voice, want.id)
		}
	}
}

// TestBuildZeroVoiceBehavesAsAnna builds without a voice and checks Anna
// speaks, so inputs built by hand keep working.
func TestBuildZeroVoiceBehavesAsAnna(t *testing.T) {
	t.Parallel()
	config := host.Build(host.Input{})
	if !strings.HasPrefix(config.SystemPrompt, "You are Anna, the host") {
		t.Fatalf("system prompt misses the Anna opening:\n%s", config.SystemPrompt)
	}
	if config.Voice != "anna" {
		t.Fatalf("config voice = %q, want anna", config.Voice)
	}
	if greeting := "Hey, I'm Anna. Welcome to your first episode! What's on your mind today?"; config.Greeting != greeting {
		t.Fatalf("greeting = %q, want %q", config.Greeting, greeting)
	}
}

// TestPromptHoldsVoiceWithoutInterrupts checks the fixed wording carries the
// new voice and never invites the host to cut in.
func TestPromptHoldsVoiceWithoutInterrupts(t *testing.T) {
	t.Parallel()
	config := host.Build(host.Input{})
	for _, want := range []string{"Energy:", "You're a host, not a therapist."} {
		if !strings.Contains(config.SystemPrompt, want) {
			t.Fatalf("system prompt misses %q:\n%s", want, config.SystemPrompt)
		}
	}
	if strings.Contains(config.SystemPrompt, "You interrupt") {
		t.Fatalf("system prompt still invites cut-ins:\n%s", config.SystemPrompt)
	}
}

// TestGreetingsForEve compares each of the three greetings as a whole
// string for Eve.
func TestGreetingsForEve(t *testing.T) {
	t.Parallel()
	eve, err := host.VoiceByID("eve")
	if err != nil {
		t.Fatalf("voice by eve error = %v, want nil", err)
	}
	first := host.Build(host.Input{Voice: eve})
	if want := "Hey, I'm Eve. Welcome to your first episode! What's on your mind today?"; first.Greeting != want {
		t.Fatalf("first greeting = %q, want %q", first.Greeting, want)
	}
	back := host.Build(host.Input{Voice: eve, PriorEpisodes: 2})
	if want := "Hey, welcome back! What's on your mind today?"; back.Greeting != want {
		t.Fatalf("returning greeting = %q, want %q", back.Greeting, want)
	}
	cited := host.Build(host.Input{
		Voice:         eve,
		PriorEpisodes: 2,
		Callback: &host.Callback{
			ID: "cb-eve",
			Mention: host.Mention{
				ID:            "m-eve",
				EpisodeNumber: 3,
				Kind:          "topic",
				Quote:         "the loft",
			},
		},
	})
	if want := `Hey, welcome back! Last time in episode 3 you mentioned "the loft", so tell me how that went.`; cited.Greeting != want {
		t.Fatalf("callback greeting = %q, want %q", cited.Greeting, want)
	}
	if cited.CallbackID != "cb-eve" {
		t.Fatalf("callback id = %q, want cb-eve", cited.CallbackID)
	}
}

// addWords stores one edit word row per token of text, in order, naming the
// speaker. The guest filter joins these rows, so this order decides what
// counts as said.
func addWords(t *testing.T, db *sql.DB, owner, episode, speaker, text string) {
	t.Helper()
	start := 0
	for i, token := range strings.Fields(text) {
		mustExec(t, db, "INSERT INTO words (id, owner_id, episode_id, text, start_ms, end_ms, source, speaker) VALUES (?, ?, ?, ?, ?, ?, 'edit', ?)",
			fmt.Sprintf("%s-%s-%d", episode, speaker, i), owner, episode, token, start, start+100, speaker)
		start += 150
	}
}

// addCallback plants one unused callback row pointing at a mention.
func addCallback(t *testing.T, db *sql.DB, id, owner, episode, mention string) {
	t.Helper()
	mustExec(t, db, "INSERT INTO callbacks (id, owner_id, episode_id, mention_id, used) VALUES (?, ?, ?, ?, 0)",
		id, owner, episode, mention)
}

// TestGuestFilterKeepsGuestWords seeds host and guest words on one labelled
// episode and checks only the guest mention reaches the prompt and the
// keyterms.
func TestGuestFilterKeepsGuestWords(t *testing.T) {
	t.Parallel()
	db := openSeason(t)
	addOwner(t, db, "owner-guest")
	addEpisode(t, db, "g-ep1", "owner-guest", 1, "ready")
	addWords(t, db, "owner-guest", "g-ep1", "host", "It was great hearing from you.")
	addWords(t, db, "owner-guest", "g-ep1", "user", "The loft was cold.")
	addMention(t, db, "g-m1", "owner-guest", "g-ep1", "topic", 1, "great hearing")
	addMention(t, db, "g-m2", "owner-guest", "g-ep1", "topic", 2, "the loft")
	config, err := host.LoadVoice(t.Context(), db, "owner-guest", "anna")
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if !strings.Contains(config.SystemPrompt, `"the loft"`) {
		t.Fatalf("system prompt misses the guest quote:\n%s", config.SystemPrompt)
	}
	if strings.Contains(config.SystemPrompt, "great hearing") {
		t.Fatalf("system prompt quotes the host:\n%s", config.SystemPrompt)
	}
	if !slices.Contains(config.Keyterms, "the loft") {
		t.Fatalf("keyterms = %q, want the guest quote", config.Keyterms)
	}
	if slices.Contains(config.Keyterms, "great hearing") {
		t.Fatalf("keyterms = %q, want no host quote", config.Keyterms)
	}
}

// TestGuestFilterSurvivesPunctuation checks a mention with an apostrophe
// survives when the guest spoke it with punctuation around it.
func TestGuestFilterSurvivesPunctuation(t *testing.T) {
	t.Parallel()
	db := openSeason(t)
	addOwner(t, db, "owner-punct")
	addEpisode(t, db, "p-ep1", "owner-punct", 1, "ready")
	addWords(t, db, "owner-punct", "p-ep1", "user", "We didn't go!")
	addMention(t, db, "p-m1", "owner-punct", "p-ep1", "topic", 1, "didn't go")
	config, err := host.LoadVoice(t.Context(), db, "owner-punct", "anna")
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if !slices.Contains(config.Keyterms, "didn't go") {
		t.Fatalf("keyterms = %q, want the guest quote", config.Keyterms)
	}
}

// TestUnlabelledEpisodeKeepsMentions seeds mentions with no edit words and
// checks every mention survives, because older episodes predate labels.
func TestUnlabelledEpisodeKeepsMentions(t *testing.T) {
	t.Parallel()
	db := openSeason(t)
	addOwner(t, db, "owner-seed")
	addEpisode(t, db, "s-ep1", "owner-seed", 1, "ready")
	addMention(t, db, "s-m1", "owner-seed", "s-ep1", "person", 1, "Maya")
	config, err := host.LoadVoice(t.Context(), db, "owner-seed", "anna")
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if !slices.Equal(config.Keyterms, []string{"Maya"}) {
		t.Fatalf("keyterms = %q, want the seeded mention", config.Keyterms)
	}
	if !strings.Contains(config.SystemPrompt, `"Maya"`) {
		t.Fatalf("system prompt misses the seeded quote:\n%s", config.SystemPrompt)
	}
}

// TestLabelledEpisodeWithoutGuestWordsKeepsNone seeds only host words on a
// labelled episode and checks its mention reaches neither the prompt nor
// the keyterms.
func TestLabelledEpisodeWithoutGuestWordsKeepsNone(t *testing.T) {
	t.Parallel()
	db := openSeason(t)
	addOwner(t, db, "owner-quiet")
	addEpisode(t, db, "q-ep1", "owner-quiet", 1, "ready")
	addWords(t, db, "owner-quiet", "q-ep1", "host", "It was great hearing from you.")
	addMention(t, db, "q-m1", "owner-quiet", "q-ep1", "topic", 1, "great hearing")
	config, err := host.LoadVoice(t.Context(), db, "owner-quiet", "anna")
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if len(config.Keyterms) != 0 {
		t.Fatalf("keyterms = %q, want none when the guest said nothing", config.Keyterms)
	}
	if strings.Contains(config.SystemPrompt, "Episode ") {
		t.Fatalf("system prompt quotes a thread the guest never said:\n%s", config.SystemPrompt)
	}
}

// TestCallbackSkipsHostOnlyQuote plants two callbacks with the older one
// quoting only host words, and checks the greeting cites the newer one
// while both rows stay unused.
func TestCallbackSkipsHostOnlyQuote(t *testing.T) {
	t.Parallel()
	db := openSeason(t)
	addOwner(t, db, "owner-cb")
	addEpisode(t, db, "c-ep1", "owner-cb", 1, "ready")
	addEpisode(t, db, "c-ep2", "owner-cb", 2, "ready")
	addEpisode(t, db, "c-ep3", "owner-cb", 3, "recording")
	addWords(t, db, "owner-cb", "c-ep1", "host", "It was great hearing from you.")
	addWords(t, db, "owner-cb", "c-ep2", "user", "The loft was cold.")
	addMention(t, db, "c-m-old", "owner-cb", "c-ep1", "topic", 1, "great hearing")
	addMention(t, db, "c-m-new", "owner-cb", "c-ep2", "topic", 2, "the loft")
	addCallback(t, db, "cb-old", "owner-cb", "c-ep3", "c-m-old")
	addCallback(t, db, "cb-new", "owner-cb", "c-ep3", "c-m-new")
	config, err := host.LoadVoice(t.Context(), db, "owner-cb", "anna")
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if want := `Hey, welcome back! Last time in episode 2 you mentioned "the loft", so tell me how that went.`; config.Greeting != want {
		t.Fatalf("greeting = %q, want %q", config.Greeting, want)
	}
	if config.CallbackID != "cb-new" {
		t.Fatalf("callback id = %q, want cb-new", config.CallbackID)
	}
	rows, err := db.QueryContext(t.Context(), "SELECT used FROM callbacks WHERE owner_id = ? ORDER BY rowid", "owner-cb")
	if err != nil {
		t.Fatalf("query callback use: %v", err)
	}
	defer rows.Close() // the rows drain below, so close reports nothing new
	var used []int
	for rows.Next() {
		var flag int
		if err := rows.Scan(&flag); err != nil {
			t.Fatalf("scan callback use: %v", err)
		}
		used = append(used, flag)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read callback use: %v", err)
	}
	if !slices.Equal(used, []int{0, 0}) {
		t.Fatalf("callback use = %v, want both rows still unused", used)
	}
}
