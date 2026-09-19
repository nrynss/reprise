// Fixture catalog, copy, receipt, and drop pins.
//
// Every test opens its own database, media store, and catalog directory.
// Catalog files are written per test, so no fixture ships beside the
// empty catalog directory. The two episode files below recur: one names
// Maya and Jonas, and the other repeats both names and plants the Maya
// quote for the next episode.

package seed_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/nrynss/keel/mediastore"
	mediasqlitestore "github.com/nrynss/keel/mediastore/sqlitestore"
	"github.com/nrynss/keel/sqlite"
	"github.com/nrynss/reprise/internal/memory"
	"github.com/nrynss/reprise/internal/seed"
	"github.com/nrynss/reprise/internal/store"
)

// entry mirrors the catalog file shape. Tests marshal it instead of
// hand writing JSON, so a renamed field fails to compile.
type entry struct {
	Title    string        `json:"title"`
	Number   int           `json:"number"`
	Audio    string        `json:"audio,omitempty"`
	Words    []wordJSON    `json:"words,omitempty"`
	Mentions []mentionJSON `json:"mentions,omitempty"`
	Planted  string        `json:"planted_quote,omitempty"`
}

// wordJSON is one transcript word in a test catalog file.
type wordJSON struct {
	Text  string `json:"text"`
	Start int    `json:"start_ms"`
	End   int    `json:"end_ms"`
}

// mentionJSON is one thread hit in a test catalog file.
type mentionJSON struct {
	Kind   string `json:"kind"`
	Offset int    `json:"word_offset"`
	Quote  string `json:"quote"`
}

// fixture carries an open seed service with its stores and directory.
type fixture struct {
	db    *sqlite.DB
	index *mediasqlitestore.Store
	media *seed.BlobStore
	svc   *seed.Service
	dir   string
}

// openService builds the diary, the media store, and the seed service on
// a temp directory. Every blob lands through the real media store, so
// survival pins measure real bytes.
func openService(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	db, err := sqlite.Open(ctx, sqlite.Config{
		Path:   filepath.Join(root, "diary.db"),
		Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := store.Open(ctx, db); err != nil {
		t.Fatalf("open diary store: %v", err)
	}
	index, err := mediasqlitestore.Open(ctx, mediasqlitestore.Config{DB: db})
	if err != nil {
		t.Fatalf("open media index: %v", err)
	}
	media, err := mediastore.Open(ctx, mediastore.Config{
		Dir:   filepath.Join(root, "media"),
		Index: index,
		ContentTypes: []string{
			"audio/ogg",
			"audio/mpeg",
			"audio/mp4",
			"audio/wav",
		},
	})
	if err != nil {
		t.Fatalf("open media store: %v", err)
	}
	var blob seed.BlobStore = media
	dir := filepath.Join(root, "season")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("make catalog dir: %v", err)
	}
	svc, err := seed.New(ctx, seed.Config{DB: db, Media: blob, CatalogDir: dir})
	if err != nil {
		t.Fatalf("open seed service: %v", err)
	}
	return &fixture{db: db, index: index, media: &blob, svc: svc, dir: dir}
}

// addUser inserts one user row of the named kind.
func (fx *fixture) addUser(t *testing.T, id, kind string) {
	t.Helper()
	if _, err := fx.db.Writer().ExecContext(t.Context(),
		"INSERT INTO users (id, kind, created_at, last_seen_at) VALUES (?, ?, 1, 1)", id, kind); err != nil {
		t.Fatalf("seed user: %v", err)
	}
}

// writeCatalog writes one catalog file with its optional audio sibling.
func (fx *fixture) writeCatalog(t *testing.T, key string, file entry, audio []byte) {
	t.Helper()
	raw, err := json.Marshal(file)
	if err != nil {
		t.Fatalf("marshal catalog file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(fx.dir, key+".json"), raw, 0o600); err != nil {
		t.Fatalf("write catalog file: %v", err)
	}
	if file.Audio != "" && audio != nil {
		if err := os.WriteFile(filepath.Join(fx.dir, file.Audio), audio, 0o600); err != nil {
			t.Fatalf("write catalog audio: %v", err)
		}
	}
}

// standardCatalog writes the two episode fixture both copy tests share.
func (fx *fixture) standardCatalog(t *testing.T) {
	t.Helper()
	fx.writeCatalog(t, "dread", entry{
		Title:  "The dreaded conversation",
		Number: 1,
		Audio:  "dread.opus",
		Words: []wordJSON{
			{Text: "Maya", Start: 0, End: 120},
			{Text: "called", Start: 120, End: 240},
		},
		Mentions: []mentionJSON{
			{Kind: "person_name", Offset: 0, Quote: "Maya"},
			{Kind: "person_name", Offset: 1, Quote: "Jonas"},
		},
	}, []byte("fake opus mix one"))
	fx.writeCatalog(t, "allotment", entry{
		Title:  "The allotment again",
		Number: 2,
		Audio:  "allotment.opus",
		Words: []wordJSON{
			{Text: "Maya", Start: 0, End: 100},
		},
		Mentions: []mentionJSON{
			{Kind: "person_name", Offset: 0, Quote: "Maya"},
			{Kind: "person_name", Offset: 0, Quote: "Jonas"},
		},
		Planted: "Maya",
	}, []byte("fake opus mix two"))
}

// sync runs the catalog import and returns its episode count.
func (fx *fixture) sync(t *testing.T) int {
	t.Helper()
	total, err := fx.svc.Sync(t.Context())
	if err != nil {
		t.Fatalf("sync catalog: %v", err)
	}
	return total
}

// copyAs ensures the copy for user and returns its episode count.
func (fx *fixture) copyAs(t *testing.T, user string) int {
	t.Helper()
	total, err := fx.svc.EnsureCopy(t.Context(), user)
	if err != nil {
		t.Fatalf("ensure copy for %s: %v", user, err)
	}
	return total
}

// episodeCount counts the episodes owned by user.
func (fx *fixture) episodeCount(t *testing.T, user string) int {
	t.Helper()
	var total int
	if err := fx.db.Reader().QueryRowContext(t.Context(),
		"SELECT COUNT(*) FROM episodes WHERE owner_id = ?", user).Scan(&total); err != nil {
		t.Fatalf("count episodes: %v", err)
	}
	return total
}

// hasReceipt reports whether the user holds a copy receipt.
func (fx *fixture) hasReceipt(t *testing.T, user string) bool {
	t.Helper()
	var found string
	err := fx.db.Reader().QueryRowContext(t.Context(),
		"SELECT user_id FROM seed_receipts WHERE user_id = ?", user).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false
	}
	if err != nil {
		t.Fatalf("read receipt: %v", err)
	}
	return true
}

// catalogBlob reads the audio blob id behind one catalog key.
func (fx *fixture) catalogBlob(t *testing.T, key string) string {
	t.Helper()
	var blob string
	if err := fx.db.Reader().QueryRowContext(t.Context(),
		"SELECT audio_blob_id FROM seed_catalog WHERE key = ?", key).Scan(&blob); err != nil {
		t.Fatalf("read catalog blob: %v", err)
	}
	return blob
}

// TestEmptyCatalogCopiesNothing pins the shipped state. A new guest sees
// no seeded episodes, holds no receipt, and can still record.
func TestEmptyCatalogCopiesNothing(t *testing.T) {
	t.Parallel()
	fx := openService(t)
	fx.addUser(t, "guest-empty", "guest")
	if total := fx.sync(t); total != 0 {
		t.Fatalf("sync = %d, want 0 on an empty directory", total)
	}
	if total := fx.copyAs(t, "guest-empty"); total != 0 {
		t.Fatalf("copy = %d, want 0 on an empty catalog", total)
	}
	if fx.hasReceipt(t, "guest-empty") {
		t.Fatal("empty catalog writes a receipt it must not write")
	}
	if fx.episodeCount(t, "guest-empty") != 0 {
		t.Fatal("empty catalog copies episodes it must not copy")
	}
}

// TestCopyGivesGuestOwnRows pins the copy shape. Two guests hold the
// same numbers and titles under ids that differ from each other and
// from the catalog. Every copy sets the seeded flag and writes a
// receipt.
func TestCopyGivesGuestOwnRows(t *testing.T) {
	t.Parallel()
	fx := openService(t)
	fx.standardCatalog(t)
	if total := fx.sync(t); total != 2 {
		t.Fatalf("sync = %d, want 2", total)
	}
	fx.addUser(t, "guest-a", "guest")
	fx.addUser(t, "guest-b", "guest")
	if total := fx.copyAs(t, "guest-a"); total != 2 {
		t.Fatalf("copy for guest-a = %d, want 2", total)
	}
	if total := fx.copyAs(t, "guest-b"); total != 2 {
		t.Fatalf("copy for guest-b = %d, want 2", total)
	}
	rows := queryEpisodes(t, fx.db, "guest-a")
	other := queryEpisodes(t, fx.db, "guest-b")
	catalog := queryEpisodes(t, fx.db, seed.SeedUserID)
	if len(rows) != 2 || len(other) != 2 || len(catalog) != 2 {
		t.Fatalf("episodes = %d, %d, catalog %d, want 2 each", len(rows), len(other), len(catalog))
	}
	for i := range rows {
		if rows[i].number != i+1 || rows[i].title != catalog[i].title {
			t.Fatalf("copy row %+v, want number %d with the catalog title", rows[i], i+1)
		}
		if !rows[i].seeded {
			t.Fatalf("copy episode %s misses the seeded flag", rows[i].id)
		}
		if rows[i].id == other[i].id || rows[i].id == catalog[i].id {
			t.Fatal("copy shares an episode id it must mint fresh")
		}
	}
	if fx.copyCount(t, "guest-a", "mentions") != 4 || fx.copyCount(t, "guest-a", "callbacks") != 1 {
		t.Fatal("copy drops mentions or the planted callback")
	}
	if !fx.hasReceipt(t, "guest-a") || !fx.hasReceipt(t, "guest-b") {
		t.Fatal("copied guests miss their receipts")
	}
}

// episodeRow is one listed episode with its copy markers.
type episodeRow struct {
	id     string
	number int
	title  string
	seeded bool
}

// queryEpisodes lists one owner episodes oldest number first.
func queryEpisodes(t *testing.T, db *sqlite.DB, owner string) []episodeRow {
	t.Helper()
	rows, err := db.Reader().QueryContext(t.Context(),
		"SELECT id, number, title, seeded FROM episodes WHERE owner_id = ? ORDER BY number ASC", owner)
	if err != nil {
		t.Fatalf("list episodes: %v", err)
	}
	defer rows.Close()
	var out []episodeRow
	for rows.Next() {
		var item episodeRow
		var seeded int
		if err := rows.Scan(&item.id, &item.number, &item.title, &seeded); err != nil {
			t.Fatalf("scan episode: %v", err)
		}
		item.seeded = seeded == 1
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("list episodes: %v", err)
	}
	return out
}

// copyCount counts one content table rows for owner.
func (fx *fixture) copyCount(t *testing.T, owner, table string) int {
	t.Helper()
	var total int
	if err := fx.db.Reader().QueryRowContext(t.Context(),
		"SELECT COUNT(*) FROM "+table+" WHERE owner_id = ?", owner).Scan(&total); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return total
}

// TestCopyRunsOnce pins the receipt. A second list copies nothing and
// changes no row.
func TestCopyRunsOnce(t *testing.T) {
	t.Parallel()
	fx := openService(t)
	fx.standardCatalog(t)
	fx.sync(t)
	fx.addUser(t, "guest-once", "guest")
	fx.copyAs(t, "guest-once")
	before := queryEpisodes(t, fx.db, "guest-once")
	if total := fx.copyAs(t, "guest-once"); total != 0 {
		t.Fatalf("second copy = %d, want 0", total)
	}
	after := queryEpisodes(t, fx.db, "guest-once")
	if len(before) != len(after) || before[0].id != after[0].id || before[1].id != after[1].id {
		t.Fatal("second list changes the copies it must keep")
	}
}

// TestDropKeepsCatalogAndNeighbor pins the rows only drop. Dropping one
// copy removes that user episode, keeps the catalog and the other
// guest intact including the catalog audio, keeps the receipt, and never
// reimports on a later list.
func TestDropKeepsCatalogAndNeighbor(t *testing.T) {
	t.Parallel()
	fx := openService(t)
	fx.standardCatalog(t)
	fx.sync(t)
	fx.addUser(t, "guest-drop-a", "guest")
	fx.addUser(t, "guest-drop-b", "guest")
	fx.copyAs(t, "guest-drop-a")
	fx.copyAs(t, "guest-drop-b")
	blob := fx.catalogBlob(t, "dread")
	rows := queryEpisodes(t, fx.db, "guest-drop-a")
	if err := fx.svc.Drop(t.Context(), "guest-drop-a", rows[0].id); err != nil {
		t.Fatalf("drop copy: %v", err)
	}
	if fx.episodeCount(t, "guest-drop-a") != 1 {
		t.Fatal("drop leaves the wrong episode count behind")
	}
	if fx.episodeCount(t, seed.SeedUserID) != 2 {
		t.Fatal("drop touches catalog rows it must keep")
	}
	if fx.episodeCount(t, "guest-drop-b") != 2 {
		t.Fatal("drop touches the other guest rows it must keep")
	}
	if _, err := fx.index.Get(t.Context(), blob); err != nil {
		t.Fatalf("catalog audio is gone after a copy drop: %v", err)
	}
	if !fx.hasReceipt(t, "guest-drop-a") {
		t.Fatal("drop clears the receipt it must keep")
	}
	if total := fx.copyAs(t, "guest-drop-a"); total != 0 {
		t.Fatalf("list after drop copies %d episodes, want 0", total)
	}
	if fx.episodeCount(t, "guest-drop-a") != 1 {
		t.Fatal("list after drop brings the seed back")
	}
	if fx.copyCount(t, "guest-drop-a", "mentions") != 2 {
		t.Fatal("drop leaves orphan mentions behind its episode")
	}
}

// TestOwnerGetsNoCopy pins the owner exclusion. Owners hold no copy and
// no receipt even when the catalog is full.
func TestOwnerGetsNoCopy(t *testing.T) {
	t.Parallel()
	fx := openService(t)
	fx.standardCatalog(t)
	fx.sync(t)
	fx.addUser(t, "owner-no-copy", "owner")
	if total := fx.copyAs(t, "owner-no-copy"); total != 0 {
		t.Fatalf("copy for owner = %d, want 0", total)
	}
	if fx.episodeCount(t, "owner-no-copy") != 0 {
		t.Fatal("owner holds copies it must not hold")
	}
	if fx.hasReceipt(t, "owner-no-copy") {
		t.Fatal("owner holds a receipt it must not hold")
	}
}

// TestEmptyAccountGetsCopy pins the forward case. A non-guest account
// with no season and no login copy exclusion receives the catalog like
// a guest does.
func TestEmptyAccountGetsCopy(t *testing.T) {
	t.Parallel()
	fx := openService(t)
	fx.standardCatalog(t)
	fx.sync(t)
	fx.addUser(t, "account-new", "member")
	if total := fx.copyAs(t, "account-new"); total != 2 {
		t.Fatalf("copy for a new account = %d, want 2", total)
	}
	for _, item := range queryEpisodes(t, fx.db, "account-new") {
		if !item.seeded {
			t.Fatalf("account copy episode %s misses the seeded flag", item.id)
		}
	}
}

// TestSeasonedUserGetsNoCopy pins the season check. A visitor who
// recorded before the catalog arrived keeps their season and gains no
// seed and no receipt.
func TestSeasonedUserGetsNoCopy(t *testing.T) {
	t.Parallel()
	fx := openService(t)
	fx.standardCatalog(t)
	fx.sync(t)
	fx.addUser(t, "guest-early", "guest")
	if _, err := fx.db.Writer().ExecContext(t.Context(), `INSERT INTO episodes
		(id, owner_id, number, title, state, visibility, share_token, seeded)
		VALUES ('early-ep', 'guest-early', 1, 'My own take', 'ready', 'private', 'token-early', 0)`); err != nil {
		t.Fatalf("seed recorded episode: %v", err)
	}
	if total := fx.copyAs(t, "guest-early"); total != 0 {
		t.Fatalf("copy for a seasoned user = %d, want 0", total)
	}
	rows := queryEpisodes(t, fx.db, "guest-early")
	if len(rows) != 1 || rows[0].id != "early-ep" {
		t.Fatalf("seasoned user holds %+v, want only their recording", rows)
	}
	if fx.hasReceipt(t, "guest-early") {
		t.Fatal("seasoned user holds a receipt it must not hold")
	}
}

// TestSignupKeepsCopies pins the id stability. Changing kind from guest
// to owner keeps the copied rows and triggers no second copy.
func TestSignupKeepsCopies(t *testing.T) {
	t.Parallel()
	fx := openService(t)
	fx.standardCatalog(t)
	fx.sync(t)
	fx.addUser(t, "guest-signup", "guest")
	fx.copyAs(t, "guest-signup")
	if _, err := fx.db.Writer().ExecContext(t.Context(),
		"UPDATE users SET kind = 'owner' WHERE id = 'guest-signup'"); err != nil {
		t.Fatalf("convert kind: %v", err)
	}
	if total := fx.copyAs(t, "guest-signup"); total != 0 {
		t.Fatalf("copy after signup = %d, want 0", total)
	}
	if fx.episodeCount(t, "guest-signup") != 2 {
		t.Fatal("signup drops the copies it must keep")
	}
}

// TestLateCatalogReachesEmptyUser pins the no receipt rule. A visitor
// with no season and no receipt gains the copy once files arrive.
func TestLateCatalogReachesEmptyUser(t *testing.T) {
	t.Parallel()
	fx := openService(t)
	fx.addUser(t, "guest-late", "guest")
	fx.sync(t)
	if total := fx.copyAs(t, "guest-late"); total != 0 {
		t.Fatalf("copy before files = %d, want 0", total)
	}
	fx.standardCatalog(t)
	fx.sync(t)
	if total := fx.copyAs(t, "guest-late"); total != 2 {
		t.Fatalf("copy after files = %d, want 2", total)
	}
}

// TestOperatorRemoveStopsNewCopies pins the retire path. Removing a file
// retires its catalog rows and audio, stops new copies of it, and keeps
// every existing copy working.
func TestOperatorRemoveStopsNewCopies(t *testing.T) {
	t.Parallel()
	fx := openService(t)
	fx.standardCatalog(t)
	fx.sync(t)
	fx.addUser(t, "guest-kept", "guest")
	fx.copyAs(t, "guest-kept")
	goneBlob := fx.catalogBlob(t, "allotment")
	keptBlob := fx.catalogBlob(t, "dread")
	if err := os.Remove(filepath.Join(fx.dir, "allotment.json")); err != nil {
		t.Fatalf("remove catalog file: %v", err)
	}
	if err := os.Remove(filepath.Join(fx.dir, "allotment.opus")); err != nil {
		t.Fatalf("remove catalog audio: %v", err)
	}
	if total := fx.sync(t); total != 1 {
		t.Fatalf("sync after remove = %d, want 1", total)
	}
	if fx.episodeCount(t, seed.SeedUserID) != 1 {
		t.Fatal("retire keeps catalog rows it must drop")
	}
	if fx.episodeCount(t, "guest-kept") != 2 {
		t.Fatal("retire drops existing copies it must keep")
	}
	if _, err := fx.index.Get(t.Context(), goneBlob); !errors.Is(err, mediastore.ErrNotFound) {
		t.Fatalf("retired audio survives with error %v, want not found", err)
	}
	if _, err := fx.index.Get(t.Context(), keptBlob); err != nil {
		t.Fatalf("kept catalog audio is gone: %v", err)
	}
	fx.addUser(t, "guest-after", "guest")
	if total := fx.copyAs(t, "guest-after"); total != 1 {
		t.Fatalf("copy after remove = %d, want 1", total)
	}
	rows := queryEpisodes(t, fx.db, "guest-after")
	if len(rows) != 1 || rows[0].number != 1 {
		t.Fatalf("copy after remove holds %+v, want only number 1", rows)
	}
}

// TestDropRefusesRecorded pins the erase branch. Recorded episodes,
// unknown ids, and other owner episodes all refuse with their own
// error, so the caller can route each one.
func TestDropRefusesRecorded(t *testing.T) {
	t.Parallel()
	fx := openService(t)
	fx.standardCatalog(t)
	fx.sync(t)
	fx.addUser(t, "guest-refuse", "guest")
	fx.addUser(t, "guest-other", "guest")
	fx.copyAs(t, "guest-refuse")
	fx.copyAs(t, "guest-other")
	if _, err := fx.db.Writer().ExecContext(t.Context(), `INSERT INTO episodes
		(id, owner_id, number, title, state, visibility, share_token, seeded)
		VALUES ('recorded-ep', 'guest-refuse', 9, 'My own take', 'ready', 'private', 'token-rec', 0)`); err != nil {
		t.Fatalf("seed recorded episode: %v", err)
	}
	if err := fx.svc.Drop(t.Context(), "guest-refuse", "recorded-ep"); !errors.Is(err, seed.ErrNotSeeded) {
		t.Fatalf("drop recorded = %v, want ErrNotSeeded", err)
	}
	other := queryEpisodes(t, fx.db, "guest-other")
	if err := fx.svc.Drop(t.Context(), "guest-refuse", other[0].id); !errors.Is(err, seed.ErrNotFound) {
		t.Fatalf("drop other owner = %v, want ErrNotFound", err)
	}
	if err := fx.svc.Drop(t.Context(), "guest-refuse", "missing-ep"); !errors.Is(err, seed.ErrNotFound) {
		t.Fatalf("drop missing = %v, want ErrNotFound", err)
	}
	if fx.episodeCount(t, "guest-refuse") != 3 {
		t.Fatal("refused drops change rows they must keep")
	}
}

// TestSeededListsCopies pins the one query branch. The flag finds every
// copy for wiring and sweeps without reading any other table.
func TestSeededListsCopies(t *testing.T) {
	t.Parallel()
	fx := openService(t)
	fx.standardCatalog(t)
	fx.sync(t)
	fx.addUser(t, "guest-listed", "guest")
	fx.copyAs(t, "guest-listed")
	ids, err := fx.svc.Seeded(t.Context(), "guest-listed")
	if err != nil {
		t.Fatalf("list seeded: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("seeded = %d ids, want 2", len(ids))
	}
	rows := queryEpisodes(t, fx.db, "guest-listed")
	if ids[0] != rows[0].id || ids[1] != rows[1].id {
		t.Fatal("seeded order skips the episode numbers")
	}
	if err := fx.svc.Drop(t.Context(), "guest-listed", ids[0]); err != nil {
		t.Fatalf("drop copy: %v", err)
	}
	ids, err = fx.svc.Seeded(t.Context(), "guest-listed")
	if err != nil {
		t.Fatalf("list seeded: %v", err)
	}
	if len(ids) != 1 {
		t.Fatalf("seeded after drop = %d ids, want 1", len(ids))
	}
}

// TestCallbackResolvesOnCopies pins the memory read path. The copied
// names group across episodes, the copied plant stays unused for the
// greeting, and the selector still opens on a copied mention.
func TestCallbackResolvesOnCopies(t *testing.T) {
	t.Parallel()
	fx := openService(t)
	fx.standardCatalog(t)
	fx.sync(t)
	fx.addUser(t, "guest-callback", "guest")
	fx.copyAs(t, "guest-callback")
	// The selector reads the resolution table beside the diary tables.
	// The memory package owns that table, so the test creates its shape
	// the way the memory migration does.
	if _, err := fx.db.Writer().ExecContext(t.Context(), `CREATE TABLE IF NOT EXISTS resolutions (
		id TEXT NOT NULL PRIMARY KEY,
		owner_id TEXT NOT NULL REFERENCES users (id),
		commitment_mention_id TEXT NOT NULL REFERENCES mentions (id) ON DELETE CASCADE,
		evidence_mention_id TEXT NOT NULL REFERENCES mentions (id) ON DELETE CASCADE
	)`); err != nil {
		t.Fatalf("create resolutions table: %v", err)
	}
	threads, err := memory.RecurringNames(t.Context(), fx.db.Writer(), "guest-callback", 2)
	if err != nil {
		t.Fatalf("recurring names: %v", err)
	}
	if len(threads) != 2 {
		t.Fatalf("threads = %d, want Maya and Jonas", len(threads))
	}
	var unused int
	if err := fx.db.Reader().QueryRowContext(t.Context(),
		"SELECT COUNT(*) FROM callbacks WHERE owner_id = 'guest-callback' AND used = 0").Scan(&unused); err != nil {
		t.Fatalf("count unused callbacks: %v", err)
	}
	if unused != 1 {
		t.Fatalf("unused callbacks = %d, want the one planted greeting", unused)
	}
	if _, err := fx.db.Writer().ExecContext(t.Context(), `INSERT INTO episodes
		(id, owner_id, number, title, state, visibility, share_token, seeded)
		VALUES ('guest-cb-ep3', 'guest-callback', 3, 'My fifth take', 'ready', 'private', 'token-cb', 0)`); err != nil {
		t.Fatalf("seed next episode: %v", err)
	}
	pick, err := memory.Select(t.Context(), fx.db.Writer(), "guest-callback", "guest-cb-ep3")
	if err != nil {
		t.Fatalf("select callback: %v", err)
	}
	if pick == nil {
		t.Fatal("selector finds nothing on a season with two copied threads")
	}
	if pick.Quote != "Jonas" || pick.EpisodeNumber != 1 {
		t.Fatalf("pick = %+v, want the copied Jonas thread in episode 1", pick)
	}
}

// TestSweepSkipsCatalogUser pins the reserved kind. The guest expiry
// query names guests only, so the catalog owner survives every sweep.
func TestSweepSkipsCatalogUser(t *testing.T) {
	t.Parallel()
	fx := openService(t)
	fx.standardCatalog(t)
	fx.sync(t)
	var kind string
	if err := fx.db.Reader().QueryRowContext(t.Context(),
		"SELECT kind FROM users WHERE id = 'seed'").Scan(&kind); err != nil {
		t.Fatalf("read catalog kind: %v", err)
	}
	if kind == "guest" {
		t.Fatal("catalog user carries the guest kind the sweep collects")
	}
	var swept []string
	rows, err := fx.db.Reader().QueryContext(t.Context(),
		"SELECT id FROM users WHERE kind = 'guest' AND last_seen_at < 2 ORDER BY id")
	if err != nil {
		t.Fatalf("run sweep query: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan sweep query: %v", err)
		}
		swept = append(swept, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("run sweep query: %v", err)
	}
	for _, id := range swept {
		if id == seed.SeedUserID {
			t.Fatal("sweep query collects the catalog user it must skip")
		}
	}
}

// TestSyncRefreshesEditedFile pins the stable key. Editing a file
// reimports its episode, and existing copies keep their snapshot.
func TestSyncRefreshesEditedFile(t *testing.T) {
	t.Parallel()
	fx := openService(t)
	fx.standardCatalog(t)
	fx.sync(t)
	before := queryEpisodes(t, fx.db, seed.SeedUserID)
	fx.addUser(t, "guest-snapshot", "guest")
	fx.copyAs(t, "guest-snapshot")
	fx.writeCatalog(t, "dread", entry{
		Title:  "The dreaded conversation, retitled",
		Number: 1,
		Audio:  "dread.opus",
		Mentions: []mentionJSON{
			{Kind: "person_name", Offset: 0, Quote: "Maya"},
			{Kind: "person_name", Offset: 1, Quote: "Jonas"},
		},
	}, []byte("fake opus mix one"))
	fx.sync(t)
	after := queryEpisodes(t, fx.db, seed.SeedUserID)
	if len(after) != 2 || after[0].title != "The dreaded conversation, retitled" {
		t.Fatalf("catalog holds %+v, want the retitled episode", after)
	}
	if after[0].id == before[0].id {
		t.Fatal("reimport keeps the episode id it must replace")
	}
	kept := queryEpisodes(t, fx.db, "guest-snapshot")
	if kept[0].title != "The dreaded conversation" {
		t.Fatalf("copy holds %q, want the snapshot title", kept[0].title)
	}
	if total := fx.copyAs(t, "guest-snapshot"); total != 0 {
		t.Fatalf("copy after edit = %d, want 0 behind the receipt", total)
	}
}

// TestSyncRejectsBadFiles pins the validation. Each defect fails the
// whole run before any row moves.
func TestSyncRejectsBadFiles(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		file  entry
		audio []byte
	}{
		"missing title":   {file: entry{Number: 1}},
		"number too low":  {file: entry{Title: "Low", Number: 0}},
		"blank quote":     {file: entry{Title: "Quote", Number: 1, Mentions: []mentionJSON{{Kind: "person_name", Quote: ""}}}},
		"dangling plant":  {file: entry{Title: "Plant", Number: 1, Planted: "Nobody"}},
		"missing audio":   {file: entry{Title: "Audio", Number: 1, Audio: "gone.opus"}},
		"bad audio shape": {file: entry{Title: "Shape", Number: 1, Audio: "mix.flac"}, audio: []byte("bytes")},
	}
	for name, item := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fx := openService(t)
			fx.writeCatalog(t, "broken", item.file, item.audio)
			_, err := fx.svc.Sync(t.Context())
			if err == nil {
				t.Fatalf("sync accepts the %s file", name)
			}
			if fx.episodeCount(t, seed.SeedUserID) != 0 {
				t.Fatal("rejected sync leaves catalog rows behind")
			}
		})
	}
}

// TestCopyUnknownUser pins the lookup. Unknown and empty ids fail
// instead of minting rows for nobody.
func TestCopyUnknownUser(t *testing.T) {
	t.Parallel()
	fx := openService(t)
	fx.standardCatalog(t)
	fx.sync(t)
	if _, err := fx.svc.EnsureCopy(t.Context(), "nobody"); !errors.Is(err, seed.ErrNotFound) {
		t.Fatalf("copy unknown = %v, want ErrNotFound", err)
	}
	if _, err := fx.svc.EnsureCopy(t.Context(), ""); !errors.Is(err, seed.ErrInvalid) {
		t.Fatalf("copy empty = %v, want ErrInvalid", err)
	}
}
