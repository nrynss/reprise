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
	wantGreeting := `Last time in episode 1 you mentioned "the talk I keep dreading with my sister", so tell me how that went.`
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
		"You ask a single question at a time",
		"You interrupt to follow up",
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
	want := "Welcome to your first episode, tell me what is on your mind today."
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
	if !strings.HasPrefix(plain.Greeting, "Welcome to your first episode") {
		t.Fatalf("greeting = %q, want the opener once the callback is used", plain.Greeting)
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
