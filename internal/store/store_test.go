package store_test

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
	"github.com/nrynss/reprise/internal/store"
)

// contentTables lists every table emptied by an episode delete.
var contentTables = []string{
	"episodes",
	"sessions",
	"stems",
	"turns",
	"words",
	"proposals",
	"decisions",
	"renders",
	"analyses",
	"mentions",
	"callbacks",
}

// openDatabase opens the file at path and closes it at cleanup. Records go
// to a discarding logger so a passing test stays quiet.
func openDatabase(t *testing.T, path string) *sqlite.DB {
	t.Helper()
	db, err := sqlite.Open(t.Context(), sqlite.Config{
		Path:   path,
		Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("open database %q: %v", path, err)
	}
	t.Cleanup(func() { _ = db.Close() }) // the handle is discarded here, so a close failure cannot fail the test
	return db
}

// openFresh opens the file at path over a new connection with no package
// pragmas. It is the independent observer the erase test queries. The sqlite
// import registers the driver, so the name resolves without a direct import
// here.
func openFresh(t *testing.T, path string) *sql.DB {
	t.Helper()
	fresh, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open(%q): %v", path, err)
	}
	t.Cleanup(func() { _ = fresh.Close() }) // the handle is discarded here, so a close failure cannot fail the test
	return fresh
}

// mustExec runs a statement on pool and fails the test on error.
func mustExec(t *testing.T, pool *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := pool.ExecContext(t.Context(), query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

// rowCount counts the rows in table on pool.
func rowCount(t *testing.T, pool *sql.DB, table string) int {
	t.Helper()
	var n int
	if err := pool.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// ledgerRows reads the migration ledger over a fresh connection and returns
// one "filename@applied_at" line per row, ordered by filename.
func ledgerRows(t *testing.T, path string) []string {
	t.Helper()
	rows, err := openFresh(t, path).Query("SELECT filename, applied_at FROM reprise_schema_migrations ORDER BY filename")
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	defer rows.Close() // the cursor is fully drained below
	var out []string
	for rows.Next() {
		var name string
		var applied int64
		if err := rows.Scan(&name, &applied); err != nil {
			t.Fatalf("scan ledger: %v", err)
		}
		out = append(out, fmt.Sprintf("%s@%d", name, applied))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	return out
}

// seedEpisode writes one owner, one episode, and one row in every content
// table. Fixed ids keep the delete and the counts exact.
func seedEpisode(t *testing.T, writer *sql.DB) {
	t.Helper()
	mustExec(t, writer, "INSERT INTO users (id, kind, created_at, last_seen_at) VALUES ('owner-1', 'owner', 1, 2)")
	mustExec(t, writer, "INSERT INTO episodes (id, owner_id, number, title, state, visibility, share_token, seeded) VALUES ('ep-1', 'owner-1', 1, 'First', 'draft', 'private', 'share-1', 0)")
	mustExec(t, writer, "INSERT INTO sessions (id, owner_id, episode_id, provider_session_id, token_cap, connected_seconds) VALUES ('session-1', 'owner-1', 'ep-1', 'provider-1', 1800, 0)")
	mustExec(t, writer, "INSERT INTO stems (id, owner_id, episode_id, media_id, role, sample_rate, start_offset_ms) VALUES ('stem-1', 'owner-1', 'ep-1', 'media-1', 'host', 48000, 0)")
	mustExec(t, writer, "INSERT INTO turns (id, owner_id, episode_id, role, text, started_ms, ended_ms, provider_item_id) VALUES ('turn-1', 'owner-1', 'ep-1', 'host', 'Hello', 0, 900, 'item-1')")
	mustExec(t, writer, "INSERT INTO words (id, owner_id, episode_id, text, start_ms, end_ms, source) VALUES ('word-1', 'owner-1', 'ep-1', 'Hello', 0, 400, 'rendered')")
	mustExec(t, writer, "INSERT INTO proposals (id, owner_id, episode_id, kind, start_word, end_word, reason) VALUES ('proposal-1', 'owner-1', 'ep-1', 'title', 0, 1, 'Names the episode')")
	mustExec(t, writer, "INSERT INTO decisions (id, owner_id, episode_id, proposal_id, decision) VALUES ('decision-1', 'owner-1', 'ep-1', 'proposal-1', 'accept')")
	mustExec(t, writer, "INSERT INTO renders (id, owner_id, episode_id, input_hash, opus_media_id, aac_media_id, loudness) VALUES ('render-1', 'owner-1', 'ep-1', 'hash-1', 'opus-1', 'aac-1', -14.0)")
	mustExec(t, writer, "INSERT INTO analyses (id, owner_id, episode_id, transcript_id, chapters, summary, entities, key_phrases) VALUES ('analysis-1', 'owner-1', 'ep-1', 'transcript-1', '[]', 'A short talk', '[]', '[]')")
	mustExec(t, writer, "INSERT INTO mentions (id, owner_id, episode_id, kind, word_offset, quote) VALUES ('mention-1', 'owner-1', 'ep-1', 'topic', 0, 'Hello')")
	mustExec(t, writer, "INSERT INTO callbacks (id, owner_id, episode_id, mention_id, used) VALUES ('callback-1', 'owner-1', 'ep-1', 'mention-1', 0)")
}

// TestMigrateAppliesTwiceWithNoChange opens the store twice and checks the
// ledger rows match exactly, so a second boot changes nothing.
func TestMigrateAppliesTwiceWithNoChange(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "diary.db")
	db := openDatabase(t, path)
	if _, err := store.Open(t.Context(), db); err != nil {
		t.Fatalf("open store: %v", err)
	}
	first := ledgerRows(t, path)
	basenames := make([]string, 0, len(first))
	for _, row := range first {
		name, _, _ := strings.Cut(row, "@")
		basenames = append(basenames, name)
	}
	if !slices.Equal(basenames, []string{"0001_schema.sql", "0002_stems_pair_unique.sql"}) {
		t.Fatalf("ledger holds %q, want both migration files", first)
	}
	if _, err := store.Open(t.Context(), db); err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	second := ledgerRows(t, path)
	if !slices.Equal(first, second) {
		t.Fatalf("ledger changed on second open: %q vs %q", first, second)
	}
}

// TestDeleteEpisodeErasesContent seeds one episode with a row in every
// content table. It deletes the episode and checks a fresh connection finds
// no row of its content in any table.
func TestDeleteEpisodeErasesContent(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "diary.db")
	db := openDatabase(t, path)
	if _, err := store.Open(t.Context(), db); err != nil {
		t.Fatalf("open store: %v", err)
	}
	seedEpisode(t, db.Writer())
	for _, table := range contentTables {
		if n := rowCount(t, db.Reader(), table); n != 1 {
			t.Fatalf("%s holds %d rows, want 1 before delete", table, n)
		}
	}
	mustExec(t, db.Writer(), "DELETE FROM episodes WHERE id = 'ep-1'")
	if err := db.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}
	fresh := openFresh(t, path)
	for _, table := range contentTables {
		var n int
		if err := fresh.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if n != 0 {
			t.Fatalf("%s holds %d rows after episode delete, want 0", table, n)
		}
	}
	var owners int
	if err := fresh.QueryRow("SELECT COUNT(*) FROM users").Scan(&owners); err != nil {
		t.Fatalf("count users: %v", err)
	}
	if owners != 1 {
		t.Fatalf("users holds %d rows, want the owner to survive", owners)
	}
}

// TestOpenRejectsNilDatabase checks the store refuses a nil database with
// its sentinel, so a caller learns the cause without matching library text.
func TestOpenRejectsNilDatabase(t *testing.T) {
	t.Parallel()
	if _, err := store.Open(t.Context(), nil); !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("open nil error = %v, want ErrInvalid", err)
	}
}
