package retention_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nrynss/keel/id"
)

// TestSweepKeepsSharedCatalogAudio pins the seeded branch of the sweep.
// An expired guest owns one seeded copy sharing the catalog render blob
// and one recorded episode. The sweep removes the guest, every guest
// row, and the recorded blobs and files, and keeps the catalog blob row
// and file with the catalog rows behind them.
func TestSweepKeepsSharedCatalogAudio(t *testing.T) {
	fx := openFixture(t)
	ctx := t.Context()
	fx.exec(t, `CREATE TABLE IF NOT EXISTS rendered_sources (
    episode_id TEXT NOT NULL PRIMARY KEY REFERENCES episodes (id) ON DELETE CASCADE,
    render_id TEXT NOT NULL REFERENCES renders (id) ON DELETE CASCADE)`)
	blob := fx.persist(t, "seed", "catalog-key", "audio/ogg", []byte("shared catalog audio"))
	fx.exec(t, `INSERT INTO users (id, kind, created_at, last_seen_at)
		VALUES ('seed', 'seed', 1, 1)`)
	catalog := "catalog-ep"
	token, err := id.New()
	if err != nil {
		t.Fatalf("mint catalog token: %v", err)
	}
	fx.exec(t, `INSERT INTO episodes
		(id, owner_id, number, title, state, visibility, share_token, seeded)
		VALUES (?, 'seed', 1, 'Catalog', 'ready', 'private', ?, 0)`, catalog, token)
	catalogRender, err := id.New()
	if err != nil {
		t.Fatalf("mint catalog render: %v", err)
	}
	fx.exec(t, `INSERT INTO renders
		(id, owner_id, episode_id, input_hash, opus_media_id, aac_media_id, loudness)
		VALUES (?, 'seed', ?, 'hash', ?, '', -23)`, catalogRender, catalog, blob)
	fx.exec(t, `INSERT INTO rendered_sources (episode_id, render_id) VALUES (?, ?)`,
		catalog, catalogRender)

	idle := testNow.Add(-testWindow).Add(-time.Hour)
	fx.seedUser(t, "guest-old", "guest", idle)
	copyID, err := id.New()
	if err != nil {
		t.Fatalf("mint copy id: %v", err)
	}
	copyToken, err := id.New()
	if err != nil {
		t.Fatalf("mint copy token: %v", err)
	}
	fx.exec(t, `INSERT INTO episodes
		(id, owner_id, number, title, state, visibility, share_token, seeded)
		VALUES (?, 'guest-old', 1, 'Copy', 'ready', 'private', ?, 1)`, copyID, copyToken)
	copyRender, err := id.New()
	if err != nil {
		t.Fatalf("mint copy render: %v", err)
	}
	fx.exec(t, `INSERT INTO renders
		(id, owner_id, episode_id, input_hash, opus_media_id, aac_media_id, loudness)
		VALUES (?, 'guest-old', ?, 'hash', ?, '', -23)`, copyRender, copyID, blob)
	fx.exec(t, `INSERT INTO rendered_sources (episode_id, render_id) VALUES (?, ?)`,
		copyID, copyRender)
	recorded := fx.seedEpisode(t, "guest-old", 2)

	jobID, err := fx.svc.Sweep(ctx)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	fx.waitSweepDone(t, jobID)

	if got := fx.count(t, "episodes", "guest-old"); got != 0 {
		t.Fatalf("episodes holds %d swept guest rows, want none", got)
	}
	if got := fx.count(t, "users", "guest-old"); got != 0 {
		t.Fatalf("users holds %d swept guest rows, want none", got)
	}
	if got := fx.sessionRows(t, "guest-old"); got != 0 {
		t.Fatalf("guest sessions holds %d swept rows, want none", got)
	}
	for _, dead := range append(append([]string{}, recorded.stems...),
		recorded.opus, recorded.aac, recorded.stereo) {
		if _, err := os.Stat(filepath.Join(fx.mediaDir, dead)); !os.IsNotExist(err) {
			t.Fatalf("recorded blob file %s still on disk (err %v)", dead, err)
		}
	}
	var mediaRows int
	if err := fx.db.Reader().QueryRowContext(ctx,
		"SELECT COUNT(*) FROM media WHERE id = ?", blob).Scan(&mediaRows); err != nil {
		t.Fatalf("count catalog blob: %v", err)
	}
	if mediaRows != 1 {
		t.Fatalf("media holds %d catalog blob rows, want the shared audio to survive", mediaRows)
	}
	if _, err := os.Stat(filepath.Join(fx.mediaDir, blob)); err != nil {
		t.Fatalf("catalog blob file missing (err %v)", err)
	}
	var catalogRows int
	if err := fx.db.Reader().QueryRowContext(ctx,
		"SELECT COUNT(*) FROM episodes WHERE id = ?", catalog).Scan(&catalogRows); err != nil {
		t.Fatalf("count catalog episode: %v", err)
	}
	if catalogRows != 1 {
		t.Fatalf("episodes holds %d catalog rows, want 1", catalogRows)
	}
	var linkRows int
	if err := fx.db.Reader().QueryRowContext(ctx,
		"SELECT COUNT(*) FROM rendered_sources WHERE episode_id = ?", catalog).Scan(&linkRows); err != nil {
		t.Fatalf("count catalog link: %v", err)
	}
	if linkRows != 1 {
		t.Fatalf("rendered_sources holds %d catalog rows, want 1", linkRows)
	}
}
