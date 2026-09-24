package retention_test

import (
	"testing"
	"time"

	"github.com/nrynss/keel/id"
)

// extraTables creates the cover, resolution, and rendered source
// tables with their production foreign keys. The suite fixture seeds
// no cover or memory rows, so these tables only exist here, where the
// keep of a finished episode is under test.
func extraTables(t *testing.T, fx *fixture) {
	t.Helper()
	fx.exec(t, `CREATE TABLE IF NOT EXISTS covers (
    id TEXT NOT NULL PRIMARY KEY,
    owner_id TEXT NOT NULL REFERENCES users (id),
    episode_id TEXT NOT NULL REFERENCES episodes (id) ON DELETE CASCADE,
    file TEXT NOT NULL,
    sha256 TEXT NOT NULL,
    width INTEGER NOT NULL,
    height INTEGER NOT NULL,
    fallback INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    UNIQUE (episode_id))`)
	fx.exec(t, `CREATE TABLE IF NOT EXISTS resolutions (
    id TEXT NOT NULL PRIMARY KEY,
    owner_id TEXT NOT NULL REFERENCES users (id),
    commitment_mention_id TEXT NOT NULL REFERENCES mentions (id) ON DELETE CASCADE,
    evidence_mention_id TEXT NOT NULL REFERENCES mentions (id) ON DELETE CASCADE)`)
	fx.exec(t, `CREATE TABLE IF NOT EXISTS rendered_sources (
    episode_id TEXT NOT NULL PRIMARY KEY REFERENCES episodes (id) ON DELETE CASCADE,
    render_id TEXT NOT NULL REFERENCES renders (id) ON DELETE CASCADE)`)
}

// seedExtras writes one cover row, one resolution link across two
// mentions, and one rendered source link for seed. The keeper takes
// all three with the episode.
func seedExtras(t *testing.T, fx *fixture, seed *episodeSeed) {
	t.Helper()
	ctx := t.Context()
	coverID, err := id.New()
	if err != nil {
		t.Fatalf("mint cover id: %v", err)
	}
	if _, err := fx.db.Writer().ExecContext(ctx, `INSERT INTO covers
		(id, owner_id, episode_id, file, sha256, width, height, fallback, created_at)
		VALUES (?, ?, ?, ?, 'sha', 512, 512, 0, 1)`,
		coverID, seed.owner, seed.episode, seed.coverFile); err != nil {
		t.Fatalf("seed cover row: %v", err)
	}
	evidenceID, err := id.New()
	if err != nil {
		t.Fatalf("mint evidence id: %v", err)
	}
	if _, err := fx.db.Writer().ExecContext(ctx, `INSERT INTO mentions
		(id, owner_id, episode_id, kind, word_offset, quote)
		VALUES (?, ?, ?, 'person', 3, 'later')`, evidenceID, seed.owner, seed.episode); err != nil {
		t.Fatalf("seed evidence mention: %v", err)
	}
	var commitmentID string
	if err := fx.db.Reader().QueryRowContext(ctx,
		"SELECT id FROM mentions WHERE episode_id = ? AND quote = 'hello'", seed.episode).Scan(&commitmentID); err != nil {
		t.Fatalf("read commitment mention: %v", err)
	}
	linkID, err := id.New()
	if err != nil {
		t.Fatalf("mint resolution id: %v", err)
	}
	if _, err := fx.db.Writer().ExecContext(ctx, `INSERT INTO resolutions
		(id, owner_id, commitment_mention_id, evidence_mention_id)
		VALUES (?, ?, ?, ?)`, linkID, seed.owner, commitmentID, evidenceID); err != nil {
		t.Fatalf("seed resolution: %v", err)
	}
	var renderID string
	if err := fx.db.Reader().QueryRowContext(ctx,
		"SELECT id FROM renders WHERE episode_id = ?", seed.episode).Scan(&renderID); err != nil {
		t.Fatalf("read render: %v", err)
	}
	if _, err := fx.db.Writer().ExecContext(ctx, `INSERT INTO rendered_sources
		(episode_id, render_id) VALUES (?, ?)`, seed.episode, renderID); err != nil {
		t.Fatalf("seed rendered source: %v", err)
	}
}

// ownerOf reads the owner column of one table row. Each table below
// keys its kept rows differently, so the caller names the column and
// the match.
func ownerOf(t *testing.T, fx *fixture, table, column, match, value string) string {
	t.Helper()
	var owner string
	if err := fx.db.Reader().QueryRowContext(t.Context(),
		"SELECT "+column+" FROM "+table+" WHERE "+match+" = ?", value).Scan(&owner); err != nil {
		t.Fatalf("read %s owner: %v", table, err)
	}
	return owner
}

// TestKeepMovesCoverAndResolutionRows pins the finished episode keep.
// The cover row and the resolution link move to the keeper with the
// episode, and the rendered source link still names the same render.
// The guest sweep then finishes, which fails on a foreign key while
// either row still names the guest.
func TestKeepMovesCoverAndResolutionRows(t *testing.T) {
	fx := openFixture(t)
	extraTables(t, fx)
	idle := testNow.Add(-testWindow).Add(-time.Hour)
	fx.seedUser(t, "owner-keeper", "owner", testNow)
	seeds := fx.seedGuest(t, "guest-gone", idle)
	kept := seeds[0]
	seedExtras(t, fx, kept)

	if err := fx.svc.Keep(t.Context(), kept.episode, "owner-keeper"); err != nil {
		t.Fatalf("keep: %v", err)
	}
	if got := ownerOf(t, fx, "covers", "owner_id", "episode_id", kept.episode); got != "owner-keeper" {
		t.Fatalf("cover owner %s, want owner-keeper", got)
	}
	var commitmentID string
	if err := fx.db.Reader().QueryRowContext(t.Context(),
		"SELECT id FROM mentions WHERE episode_id = ? AND quote = 'hello'", kept.episode).Scan(&commitmentID); err != nil {
		t.Fatalf("read commitment mention: %v", err)
	}
	if got := ownerOf(t, fx, "resolutions", "owner_id", "commitment_mention_id", commitmentID); got != "owner-keeper" {
		t.Fatalf("resolution owner %s, want owner-keeper", got)
	}
	var renderID, linkedRender string
	if err := fx.db.Reader().QueryRowContext(t.Context(),
		"SELECT id FROM renders WHERE episode_id = ?", kept.episode).Scan(&renderID); err != nil {
		t.Fatalf("read render: %v", err)
	}
	if err := fx.db.Reader().QueryRowContext(t.Context(),
		"SELECT render_id FROM rendered_sources WHERE episode_id = ?", kept.episode).Scan(&linkedRender); err != nil {
		t.Fatalf("read rendered source: %v", err)
	}
	if linkedRender != renderID {
		t.Fatalf("rendered source names %s, want %s", linkedRender, renderID)
	}

	jobID, err := fx.svc.Sweep(t.Context())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	fx.waitSweepDone(t, jobID)

	if got := fx.count(t, "episodes", "owner-keeper"); got != 1 {
		t.Fatalf("episodes holds %d kept rows, want 1", got)
	}
	if got := fx.count(t, "users", "guest-gone"); got != 0 {
		t.Fatalf("users holds %d guest rows, want none", got)
	}
}
