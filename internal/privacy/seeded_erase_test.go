package privacy_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nrynss/keel/id"
)

// TestEraseKeepsSharedCatalogAudio pins the seeded branch of the
// erasure inventory. Two seeded copies share one catalog render blob
// the way the seeded season copies it. Erasing one copy removes its
// episode, render, and link rows and keeps the catalog blob row, the
// catalog rows, and the other copy playable.
func TestEraseKeepsSharedCatalogAudio(t *testing.T) {
	fx := openFixture(t)
	ctx := t.Context()
	fx.exec(t, `CREATE TABLE IF NOT EXISTS rendered_sources (
    episode_id TEXT NOT NULL PRIMARY KEY REFERENCES episodes (id) ON DELETE CASCADE,
    render_id TEXT NOT NULL REFERENCES renders (id) ON DELETE CASCADE)`)
	blob := fx.persist(t, "seed", "catalog-key", "audio/ogg", []byte("shared catalog audio"))
	fx.exec(t, `INSERT INTO users (id, kind, created_at, last_seen_at)
		VALUES ('seed', 'seed', 1, 1), ('guest-a', 'guest', 1, 1), ('guest-b', 'guest', 1, 1)`)
	catalog := "catalog-ep"
	fx.exec(t, `INSERT INTO episodes
		(id, owner_id, number, title, state, visibility, share_token, seeded)
		VALUES (?, 'seed', 1, 'Catalog', 'ready', 'private', 'token-catalog', 0)`, catalog)
	catalogRender := mintID(t)
	fx.exec(t, `INSERT INTO renders
		(id, owner_id, episode_id, input_hash, opus_media_id, aac_media_id, loudness)
		VALUES (?, 'seed', ?, 'hash', ?, '', -23)`, catalogRender, catalog, blob)
	fx.exec(t, `INSERT INTO rendered_sources (episode_id, render_id) VALUES (?, ?)`,
		catalog, catalogRender)
	copyA := seedCopy(t, fx, "guest-a", 1, blob)
	copyB := seedCopy(t, fx, "guest-b", 1, blob)

	fx.as("guest-a")
	jobID, err := fx.svc.Erase(ctx, copyA)
	if err != nil {
		t.Fatalf("erase seeded copy: %v", err)
	}
	fx.waitJobDone(t, jobID)

	if got := countWhere(t, fx, "episodes", "id", copyA); got != 0 {
		t.Fatalf("episodes holds %d copy-a rows, want none", got)
	}
	if got := countWhere(t, fx, "renders", "episode_id", copyA); got != 0 {
		t.Fatalf("renders holds %d copy-a rows, want none", got)
	}
	if got := countWhere(t, fx, "rendered_sources", "episode_id", copyA); got != 0 {
		t.Fatalf("rendered_sources holds %d copy-a rows, want none", got)
	}
	if got := countWhere(t, fx, "media", "id", blob); got != 1 {
		t.Fatalf("media holds %d catalog blob rows, want the shared audio to survive", got)
	}
	if _, err := os.Stat(filepath.Join(fx.mediaDir, blob)); err != nil {
		t.Fatalf("catalog blob file missing (err %v)", err)
	}
	if got := countWhere(t, fx, "episodes", "id", catalog); got != 1 {
		t.Fatalf("episodes holds %d catalog rows, want 1", got)
	}
	if got := countWhere(t, fx, "renders", "episode_id", catalog); got != 1 {
		t.Fatalf("renders holds %d catalog rows, want 1", got)
	}
	if got := countWhere(t, fx, "rendered_sources", "episode_id", catalog); got != 1 {
		t.Fatalf("rendered_sources holds %d catalog rows, want 1", got)
	}
	if got := countWhere(t, fx, "episodes", "id", copyB); got != 1 {
		t.Fatalf("episodes holds %d copy-b rows, want the other copy to survive", got)
	}
	var opus string
	if err := fx.db.Reader().QueryRowContext(ctx,
		"SELECT opus_media_id FROM renders WHERE episode_id = ?", copyB).Scan(&opus); err != nil {
		t.Fatalf("read copy-b render: %v", err)
	}
	if opus != blob {
		t.Fatalf("copy-b render names blob %s, want the shared catalog audio", opus)
	}
	if _, err := os.Stat(filepath.Join(fx.mediaDir, opus)); err != nil {
		t.Fatalf("copy-b audio file missing (err %v)", err)
	}
}

// seedCopy writes one seeded copy episode with a render row and a link
// row behind the shared blob, the shape the seeded season copies.
func seedCopy(t *testing.T, fx *fixture, owner string, number int64, blob string) string {
	t.Helper()
	episodeID := mintID(t)
	token := mintID(t)
	fx.exec(t, `INSERT INTO episodes
		(id, owner_id, number, title, state, visibility, share_token, seeded)
		VALUES (?, ?, ?, 'Copy', 'ready', 'private', ?, 1)`, episodeID, owner, number, token)
	renderID := mintID(t)
	fx.exec(t, `INSERT INTO renders
		(id, owner_id, episode_id, input_hash, opus_media_id, aac_media_id, loudness)
		VALUES (?, ?, ?, 'hash', ?, '', -23)`, renderID, owner, episodeID, blob)
	fx.exec(t, `INSERT INTO rendered_sources (episode_id, render_id) VALUES (?, ?)`,
		episodeID, renderID)
	return episodeID
}

// mintID mints one unguessable id.
func mintID(t *testing.T) string {
	t.Helper()
	next, err := id.New()
	if err != nil {
		t.Fatalf("mint id: %v", err)
	}
	return next
}

// countWhere returns the rows in table where column matches value.
func countWhere(t *testing.T, fx *fixture, table, column, value string) int {
	t.Helper()
	var total int
	if err := fx.db.Reader().QueryRowContext(t.Context(),
		"SELECT COUNT(*) FROM "+table+" WHERE "+column+" = ?", value).Scan(&total); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return total
}
