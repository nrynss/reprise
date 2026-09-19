package analysis_test

import (
	"database/sql"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/nrynss/keel/sqlite"
	"github.com/nrynss/reprise/internal/store"
)

// openDiary migrates a fresh diary file and returns its writer. Records
// go to a discarding logger so a passing test stays quiet.
func openDiary(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sqlite.Open(t.Context(), sqlite.Config{
		Path:   filepath.Join(t.TempDir(), "diary.db"),
		Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := store.Open(t.Context(), db); err != nil {
		t.Fatalf("open store: %v", err)
	}
	return db.Writer()
}

// mustExec runs a statement and fails the test on error.
func mustExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, args)
	}
}

// addOwner writes one user row.
func addOwner(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	mustExec(t, db, "INSERT INTO users (id, kind, created_at, last_seen_at) VALUES (?, 'guest', 1, 2)", id)
}

// addEpisode writes one episode row.
func addEpisode(t *testing.T, db *sql.DB, id, owner string, number int, state string) {
	t.Helper()
	mustExec(t, db, "INSERT INTO episodes (id, owner_id, number, title, state, visibility, share_token, seeded) VALUES (?, ?, ?, ?, ?, 'private', ?, 0)",
		id, owner, number, "Episode", state, "share-"+id)
}

// testdataRead reads one committed fixture file.
func testdataRead(t *testing.T, name string) ([]byte, error) {
	t.Helper()
	return os.ReadFile(filepath.Join("..", "..", "testdata", name))
}

// fixtureRender reads real stem bytes from the committed sessions. The
// upload seam carries them like render bytes, so the run test moves
// genuine audio without a microphone or a fixture render file.
func fixtureRender(t *testing.T) []byte {
	t.Helper()
	raw, err := testdataRead(t, "sessions/steady/user-a.wav")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return raw
}
