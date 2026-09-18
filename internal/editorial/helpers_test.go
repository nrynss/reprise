package editorial_test

import (
	"bytes"
	"context"
	"database/sql"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"

	"github.com/nrynss/keel/cost"
	"github.com/nrynss/keel/sqlite"
	"github.com/nrynss/reprise/internal/gemini"
	"github.com/nrynss/reprise/internal/store"
)

// openDiary migrates a fresh diary file and returns its writer. Records go
// to a discarding logger so a passing test stays quiet.
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
func addEpisode(t *testing.T, db *sql.DB, id, owner string, number int) {
	t.Helper()
	mustExec(t, db, "INSERT INTO episodes (id, owner_id, number, title, state, visibility, share_token, seeded) VALUES (?, ?, ?, ?, 'draft', 'private', ?, 0)",
		id, owner, number, "Draft", "share-"+id)
}

// addWords writes count edit words spaced stepMs apart, each lenMs long,
// so spans measure exact durations with no fixture and no microphone.
func addWords(t *testing.T, db *sql.DB, owner, episode string, count int, stepMs, lenMs int64) {
	t.Helper()
	for i := 0; i < count; i++ {
		start := int64(i) * stepMs
		mustExec(t, db, "INSERT INTO words (id, owner_id, episode_id, text, start_ms, end_ms, source) VALUES (?, ?, ?, ?, ?, ?, 'edit')",
			"word-"+episode+"-"+itoa(i), owner, episode, "w"+itoa(i), start, start+lenMs)
	}
}

// itoa formats a small int without importing strconv in every test.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var out []byte
	for n := i; n > 0; n /= 10 {
		out = append([]byte{byte('0' + n%10)}, out...)
	}
	return string(out)
}

// fakeBudget records every hold and frees nothing silently.
type fakeBudget struct {
	mu       sync.Mutex
	reserved []cost.Price
	settled  [][2]cost.Price
	released []cost.Price
	refuse   error
}

func (b *fakeBudget) Reserve(p cost.Price) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.reserved = append(b.reserved, p)
	return b.refuse
}

func (b *fakeBudget) Settle(held, actual cost.Price) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.settled = append(b.settled, [2]cost.Price{held, actual})
	return nil
}

func (b *fakeBudget) Release(p cost.Price) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.released = append(b.released, p)
}

// scriptedModel answers one canned JSON and records the request it heard.
type scriptedModel struct {
	mu       sync.Mutex
	calls    int
	answer   string
	err      error
	timeline string
	userLen  int
	hostLen  int
	model    string
}

func (m *scriptedModel) GenerateEditorial(_ context.Context, model string, req gemini.EditorialRequest) (gemini.EditorialAnswer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	m.model, m.timeline, m.userLen, m.hostLen = model, req.Timeline, len(req.UserStem), len(req.HostStem)
	if m.err != nil {
		return gemini.EditorialAnswer{}, m.err
	}
	return gemini.EditorialAnswer{JSON: m.answer}, nil
}

// captureLog returns a logger writing warnings to a buffer the test reads.
func captureLog() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewTextHandler(&buf, nil)), &buf
}

// rowCount counts the rows in table, optionally filtered by episode.
func rowCount(t *testing.T, db *sql.DB, table, episode string) int {
	t.Helper()
	var n int
	query := "SELECT COUNT(*) FROM " + table
	var err error
	if episode == "" {
		err = db.QueryRowContext(t.Context(), query).Scan(&n)
	} else {
		err = db.QueryRowContext(t.Context(), query+" WHERE episode_id = ?", episode).Scan(&n)
	}
	if err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}
