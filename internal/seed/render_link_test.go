// Pins for the render row and its link row on catalog and copy rows.
//
// The detail sends rendered words only when the link names the newest
// render, so a seeded copy that adds a render row must add its link row
// beside it. These tests read the same two queries the detail reads.

package seed_test

import (
	"errors"
	"testing"

	"github.com/nrynss/reprise/internal/analysis"
	"github.com/nrynss/reprise/internal/episode"
	"github.com/nrynss/reprise/internal/seed"
)

// renderOf reads the newest render id and opus blob behind one episode,
// the way the detail playback does.
func renderOf(t *testing.T, fx *fixture, owner, episodeID string) (string, string) {
	t.Helper()
	latest, err := episode.NewestRenderID(t.Context(), fx.db, episodeID)
	if err != nil {
		t.Fatalf("newest render: %v", err)
	}
	if latest == "" {
		return "", ""
	}
	var blob string
	if err := fx.db.Reader().QueryRowContext(t.Context(),
		`SELECT opus_media_id FROM renders WHERE id = ?`, latest).Scan(&blob); err != nil {
		t.Fatalf("render blob: %v", err)
	}
	return latest, blob
}

// TestCatalogRenderLinksWords pins the import link. A file with an audio
// sibling stores one render row and the link names it, so the stored
// rendered words resolve against the render the detail plays.
func TestCatalogRenderLinksWords(t *testing.T) {
	t.Parallel()
	fx := openService(t)
	fx.standardCatalog(t)
	fx.sync(t)
	rows := queryEpisodes(t, fx.db, seed.SeedUserID)
	if len(rows) != 2 {
		t.Fatalf("catalog episodes = %d, want 2", len(rows))
	}
	for _, item := range rows {
		latest, blob := renderOf(t, fx, seed.SeedUserID, item.id)
		if latest == "" || blob == "" {
			t.Fatalf("catalog episode %d stores no playable render", item.number)
		}
		source, err := analysis.RenderedSource(t.Context(), fx.db.Writer(), item.id)
		if err != nil {
			t.Fatalf("rendered source: %v", err)
		}
		if source != latest {
			t.Fatal("catalog link names a render the detail never plays")
		}
	}
	if _, err := fx.index.Get(t.Context(), fx.catalogBlob(t, "dread")); err != nil {
		t.Fatalf("catalog audio is gone: %v", err)
	}
}

// TestCopyRenderKeepsLink pins the copy link. Each copy mints its own
// render row under the new owner, keeps the shared blob, and links its
// own words to its own render.
func TestCopyRenderKeepsLink(t *testing.T) {
	t.Parallel()
	fx := openService(t)
	fx.standardCatalog(t)
	fx.sync(t)
	fx.addUser(t, "guest-render", "guest")
	fx.copyAs(t, "guest-render")
	rows := queryEpisodes(t, fx.db, "guest-render")
	catalog := queryEpisodes(t, fx.db, seed.SeedUserID)
	if len(rows) != 2 {
		t.Fatalf("copy episodes = %d, want 2", len(rows))
	}
	for i, item := range rows {
		latest, blob := renderOf(t, fx, "guest-render", item.id)
		if latest == "" || blob == "" {
			t.Fatalf("copy episode %d stores no playable render", item.number)
		}
		_, catalogBlob := renderOf(t, fx, seed.SeedUserID, catalog[i].id)
		if blob != catalogBlob {
			t.Fatal("copy render points at bytes the catalog never stored")
		}
		source, err := analysis.RenderedSource(t.Context(), fx.db.Writer(), item.id)
		if err != nil {
			t.Fatalf("rendered source: %v", err)
		}
		if source != latest {
			t.Fatal("copy link names a render the detail never plays")
		}
		var other string
		if err := fx.db.Reader().QueryRowContext(t.Context(),
			`SELECT id FROM renders WHERE episode_id = ?`, catalog[i].id).Scan(&other); err != nil {
			t.Fatalf("catalog render: %v", err)
		}
		if other == latest {
			t.Fatal("copy shares the catalog render id it must mint fresh")
		}
	}
}

// TestRowsOnlyFileStoresNoRender pins the audio-less file. Words without
// a render store no render row and no link, so the detail keeps them
// back the way it does for a draft with no render.
func TestRowsOnlyFileStoresNoRender(t *testing.T) {
	t.Parallel()
	fx := openService(t)
	fx.writeCatalog(t, "rows", entry{
		Title:  "Rows without audio",
		Number: 1,
		Words:  []wordJSON{{Text: "Maya", Start: 0, End: 120}},
		Mentions: []mentionJSON{
			{Kind: "person_name", Offset: 0, Quote: "Maya"},
		},
	}, nil)
	fx.sync(t)
	fx.addUser(t, "guest-rows", "guest")
	fx.copyAs(t, "guest-rows")
	for _, owner := range []string{seed.SeedUserID, "guest-rows"} {
		rows := queryEpisodes(t, fx.db, owner)
		if len(rows) != 1 {
			t.Fatalf("episodes for %s = %d, want 1", owner, len(rows))
		}
		latest, err := episode.NewestRenderID(t.Context(), fx.db, rows[0].id)
		if err != nil {
			t.Fatalf("newest render: %v", err)
		}
		if latest != "" {
			t.Fatalf("rows only episode for %s stores render %s it must not store", owner, latest)
		}
		source, err := analysis.RenderedSource(t.Context(), fx.db.Writer(), rows[0].id)
		if err != nil {
			t.Fatalf("rendered source: %v", err)
		}
		if source != "" {
			t.Fatalf("rows only episode for %s links render %s it must not link", owner, source)
		}
	}
}

// TestDropRemovesCopyRender pins the rows only drop for renders. Dropping
// a copy removes its render and link rows, and keeps the catalog render
// with its audio.
func TestDropRemovesCopyRender(t *testing.T) {
	t.Parallel()
	fx := openService(t)
	fx.standardCatalog(t)
	fx.sync(t)
	fx.addUser(t, "guest-render-drop", "guest")
	fx.copyAs(t, "guest-render-drop")
	blob := fx.catalogBlob(t, "dread")
	rows := queryEpisodes(t, fx.db, "guest-render-drop")
	if err := fx.svc.Drop(t.Context(), "guest-render-drop", rows[0].id); err != nil {
		t.Fatalf("drop copy: %v", err)
	}
	var left int
	if err := fx.db.Reader().QueryRowContext(t.Context(),
		`SELECT COUNT(*) FROM renders WHERE episode_id = ?`, rows[0].id).Scan(&left); err != nil {
		t.Fatalf("count dropped renders: %v", err)
	}
	if left != 0 {
		t.Fatalf("dropped copy keeps %d render rows it must drop", left)
	}
	if _, err := fx.index.Get(t.Context(), blob); err != nil {
		t.Fatalf("catalog audio is gone after a copy drop: %v", err)
	}
	catalog := queryEpisodes(t, fx.db, seed.SeedUserID)
	latest, _ := renderOf(t, fx, seed.SeedUserID, catalog[0].id)
	if latest == "" {
		t.Fatal("drop touches the catalog render it must keep")
	}
}

// TestWelcomeReadsCopiesFirst pins the first visit read. A guest with
// copies opens on their latest copy, an owner opens on the catalog, and
// an empty catalog reports no teaser.
func TestWelcomeReadsCopiesFirst(t *testing.T) {
	t.Parallel()
	fx := openService(t)
	fx.standardCatalog(t)
	fx.sync(t)
	fx.addUser(t, "guest-teaser", "guest")
	fx.addUser(t, "owner-teaser", "owner")
	fx.copyAs(t, "guest-teaser")
	guest, err := fx.svc.Welcome(t.Context(), "guest-teaser")
	if err != nil {
		t.Fatalf("guest welcome: %v", err)
	}
	if guest.Episodes != 2 || guest.Number != 2 || guest.Title != "The allotment again" {
		t.Fatalf("guest welcome = %+v, want two copies opening on number 2", guest)
	}
	if guest.FirstLine != "Maya" || guest.SecondLine != "Jonas" {
		t.Fatalf("guest lines = %q, %q, want the catalog mention quotes", guest.FirstLine, guest.SecondLine)
	}
	if guest.AudioBlobID == "" {
		t.Fatal("guest teaser names no render audio")
	}
	owner, err := fx.svc.Welcome(t.Context(), "owner-teaser")
	if err != nil {
		t.Fatalf("owner welcome: %v", err)
	}
	if owner.Episodes != 2 || owner.Number != 2 || owner.AudioBlobID != guest.AudioBlobID {
		t.Fatalf("owner welcome = %+v, want the catalog behind the same audio", owner)
	}
	if _, err := fx.svc.Welcome(t.Context(), ""); !errors.Is(err, seed.ErrInvalid) {
		t.Fatalf("empty welcome = %v, want ErrInvalid", err)
	}
}

// TestWelcomeEmptyCatalog pins the empty read. No files means no teaser
// and no count, so the screen offers only the record button.
func TestWelcomeEmptyCatalog(t *testing.T) {
	t.Parallel()
	fx := openService(t)
	fx.sync(t)
	fx.addUser(t, "guest-bare", "guest")
	got, err := fx.svc.Welcome(t.Context(), "guest-bare")
	if err != nil {
		t.Fatalf("bare welcome: %v", err)
	}
	if got != (seed.Welcome{}) {
		t.Fatalf("bare welcome = %+v, want zero", got)
	}
}

// TestSeededLinksDoNotLeakAcrossOwners pins the owner scope on the link
// read. The rendered source names an episode id, and the copy episode id
// differs from the catalog one, so a catalog reimport never repoints a
// copy.
func TestSeededLinksDoNotLeakAcrossOwners(t *testing.T) {
	t.Parallel()
	fx := openService(t)
	fx.standardCatalog(t)
	fx.sync(t)
	fx.addUser(t, "guest-scope", "guest")
	fx.copyAs(t, "guest-scope")
	rows := queryEpisodes(t, fx.db, "guest-scope")
	catalog := queryEpisodes(t, fx.db, seed.SeedUserID)
	before, err := analysis.RenderedSource(t.Context(), fx.db.Writer(), rows[0].id)
	if err != nil {
		t.Fatalf("copy source: %v", err)
	}
	fx.writeCatalog(t, "dread", entry{
		Title:  "The dreaded conversation, retitled",
		Number: 1,
		Audio:  "dread.opus",
		Words:  []wordJSON{{Text: "Maya", Start: 0, End: 120}},
		Mentions: []mentionJSON{
			{Kind: "person_name", Offset: 0, Quote: "Maya"},
		},
	}, []byte("fake opus mix one, revised"))
	fx.sync(t)
	after, err := analysis.RenderedSource(t.Context(), fx.db.Writer(), rows[0].id)
	if err != nil {
		t.Fatalf("copy source after reimport: %v", err)
	}
	if after != before {
		t.Fatal("catalog reimport repoints the copy link it must keep")
	}
	refreshed := queryEpisodes(t, fx.db, seed.SeedUserID)
	if refreshed[0].id == catalog[0].id {
		t.Fatal("reimport keeps the catalog episode id it must replace")
	}
}
