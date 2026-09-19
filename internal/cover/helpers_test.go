// Test helpers open a diary database and seed the rows the cover pass
// reads. Records go to a discarding logger so a passing test stays
// quiet.

package cover_test

import (
	"bytes"
	"context"
	"database/sql"
	"image"
	"image/color"
	"image/png"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"

	"github.com/nrynss/keel/cost"
	"github.com/nrynss/keel/sqlite"
	"github.com/nrynss/reprise/internal/cover"
	"github.com/nrynss/reprise/internal/store"
)

// openDiary migrates a fresh diary file and returns its writer.
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

// seedEpisode writes one owner, one episode, and its title and notes
// proposals, so the pass briefs the model from stored rows.
func seedEpisode(t *testing.T, db *sql.DB, id, owner string, number int, title, notes string) {
	t.Helper()
	mustExec(t, db, "INSERT INTO users (id, kind, created_at, last_seen_at) VALUES (?, 'guest', 1, 2)", owner)
	mustExec(t, db, "INSERT INTO episodes (id, owner_id, number, title, state, visibility, share_token, seeded) VALUES (?, ?, ?, 'Draft', 'draft', 'private', ?, 0)",
		id, owner, number, "share-"+id)
	mustExec(t, db, "INSERT INTO proposals (id, owner_id, episode_id, kind, start_word, end_word, reason) VALUES (?, ?, ?, 'title', 0, 0, ?)",
		"prop-title-"+id, owner, id, title)
	mustExec(t, db, "INSERT INTO proposals (id, owner_id, episode_id, kind, start_word, end_word, reason) VALUES (?, ?, ?, 'show_notes', 0, 0, ?)",
		"prop-notes-"+id, owner, id, notes)
}

// runCfg builds the run inputs the tests share.
func runCfg(t *testing.T, db *sql.DB, model *cover.ScriptedModel, budgets *fakeBudget, receipt *[]byte) cover.Config {
	return cover.Config{
		DB:        db,
		Model:     model,
		Budgets:   budgets,
		ModelID:   "image-model",
		OwnerID:   "owner-a",
		EpisodeID: "ep-1",
		Dir:       filepath.Join(t.TempDir(), "covers"),
		SaveRaw: func(_ context.Context, raw []byte) error {
			*receipt = append([]byte(nil), raw...)
			return nil
		},
	}
}

// coverRow reads the stored cover row for one episode.
func coverRow(t *testing.T, db *sql.DB, episode string) (file, sha string, width, height, fallback int) {
	t.Helper()
	if err := db.QueryRowContext(t.Context(),
		"SELECT file, sha256, width, height, fallback FROM covers WHERE episode_id = ?", episode).Scan(&file, &sha, &width, &height, &fallback); err != nil {
		t.Fatalf("read cover: %v", err)
	}
	return file, sha, width, height, fallback
}

// solidPNG encodes one flat panel of the given size.
func solidPNG(t *testing.T, edge int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, edge, edge))
	for y := 0; y < edge; y++ {
		for x := 0; x < edge; x++ {
			img.Set(x, y, color.RGBA{90, 90, 90, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode solid: %v", err)
	}
	return buf.Bytes()
}

// rectPNG encodes one non square panel.
func rectPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 512, 256))
	for y := 0; y < 256; y++ {
		for x := 0; x < 512; x++ {
			img.Set(x, y, color.RGBA{uint8(x % 256), uint8(y % 256), 120, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode rect: %v", err)
	}
	return buf.Bytes()
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
