package api

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nrynss/keel/sqlite"
	"github.com/nrynss/reprise/internal/analysis"
)

// seedWordRow writes one word and fails the test on error.
func seedWordRow(t *testing.T, db *sqlite.DB, id, owner, episodeID, text, source string, startMs, endMs int64) {
	t.Helper()
	if _, err := db.Writer().ExecContext(t.Context(),
		`INSERT INTO words (id, owner_id, episode_id, text, start_ms, end_ms, source)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, owner, episodeID, text, startMs, endMs, source); err != nil {
		t.Fatalf("seed word %s: %v", id, err)
	}
}

// seedStemRow writes one stem and fails the test on error.
func seedStemRow(t *testing.T, db *sqlite.DB, id, owner, episodeID, mediaID, role string) {
	t.Helper()
	if _, err := db.Writer().ExecContext(t.Context(),
		`INSERT INTO stems (id, owner_id, episode_id, media_id, role, sample_rate, start_offset_ms)
		 VALUES (?, ?, ?, ?, ?, 48000, 0)`,
		id, owner, episodeID, mediaID, role); err != nil {
		t.Fatalf("seed stem %s: %v", id, err)
	}
}

// seedRenderRow writes one render and fails the test on error.
func seedRenderRow(t *testing.T, db *sqlite.DB, id, owner, episodeID, opusID string) {
	t.Helper()
	if _, err := db.Writer().ExecContext(t.Context(),
		`INSERT INTO renders (id, owner_id, episode_id, input_hash, opus_media_id, aac_media_id, loudness)
		 VALUES (?, ?, ?, ?, ?, 'aac', -16)`,
		id, owner, episodeID, "hash-"+id, opusID); err != nil {
		t.Fatalf("seed render %s: %v", id, err)
	}
}

// secondsNear reports whether two second values match within a millisecond.
func secondsNear(got, want float64) bool {
	return math.Abs(got-want) < 0.000001
}

// stubPreview answers the preview read with one fixed id, so the detail
// test pins the address mapping without opening the preview table.
type stubPreview struct {
	id string
}

// PreviewMediaID returns the fixed preview id.
func (s stubPreview) PreviewMediaID(_ context.Context, _, _ string) (string, error) {
	return s.id, nil
}

// TestEpisodeDetailServesPreviewAddress requires the detail to carry the
// preview address once a preview row exists, and to leave it empty while
// none exists. The editor plays the preview first, because it carries
// both voices on the word clock.
func TestEpisodeDetailServesPreviewAddress(t *testing.T) {
	t.Parallel()
	db, guests := openDiary(t)
	cookie, owner := mintGuest(t, guests)
	seedEpisodeRow(t, db, "ep-1", owner.ID, 1, "draft")
	seedStemRow(t, db, "stem-user", owner.ID, "ep-1", "user-blob", "user")

	detail := func(handler http.Handler, id string) episodeDetailJSON {
		t.Helper()
		rec := serve(guests, handler, cookie, httptest.NewRequest(http.MethodGet, "/api/episodes/"+id, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("detail %s status = %d, want 200", id, rec.Code)
		}
		var body episodeDetailJSON
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("decode detail %s: %v", id, err)
		}
		return body
	}

	bare := detail(NewEpisodes(newEpisodeService(t, db, nil).svc), "ep-1")
	if bare.PreviewAudioURL != "" {
		t.Fatalf("preview address = %q, want empty with no preview reader", bare.PreviewAudioURL)
	}
	without := detail(NewEpisodesWithPreview(newEpisodeService(t, db, nil).svc, stubPreview{}), "ep-1")
	if without.PreviewAudioURL != "" {
		t.Fatalf("preview address = %q, want empty with no preview row", without.PreviewAudioURL)
	}
	with := detail(NewEpisodesWithPreview(newEpisodeService(t, db, nil).svc, stubPreview{id: "preview-blob"}), "ep-1")
	if with.PreviewAudioURL != "/media/preview-blob" {
		t.Fatalf("preview address = %q, want the preview blob", with.PreviewAudioURL)
	}
	if with.AudioURL != "/media/user-blob" {
		t.Fatalf("audio address = %q, want the stem beside the preview", with.AudioURL)
	}
}

// shareFixture wraps an episode store with fixed share tokens, so the
// detail test pins the share path without opening the publish tables.
type shareFixture struct {
	episodeStore
	tokens map[string]string
}

// ShareToken returns the fixed token for one episode, or empty when the
// episode holds none.
func (s shareFixture) ShareToken(_ context.Context, _, episodeID string) (string, error) {
	return s.tokens[episodeID], nil
}

// TestEpisodeDetailCarriesSharePathWhilePublic requires the detail to
// carry /share/<token> on a public episode and an empty share path on a
// private one. A private episode keeps its rotated token hidden, because
// the visibility check runs before the token read.
func TestEpisodeDetailCarriesSharePathWhilePublic(t *testing.T) {
	t.Parallel()
	db, guests := openDiary(t)
	cookie, owner := mintGuest(t, guests)
	seedEpisodeRow(t, db, "ep-public", owner.ID, 1, "ready")
	seedEpisodeRow(t, db, "ep-private", owner.ID, 2, "ready")
	if _, err := db.Writer().ExecContext(t.Context(),
		"UPDATE episodes SET visibility = 'public', share_token = ? WHERE id = ?",
		"token-1", "ep-public"); err != nil {
		t.Fatalf("publish ep-public: %v", err)
	}
	if _, err := db.Writer().ExecContext(t.Context(),
		"UPDATE episodes SET share_token = ? WHERE id = ?",
		"token-2", "ep-private"); err != nil {
		t.Fatalf("rotate ep-private: %v", err)
	}

	stack := newEpisodeService(t, db, nil)
	handler := NewEpisodesWithShare(stack.svc, nil, shareFixture{
		episodeStore: stack.svc,
		tokens:       map[string]string{"ep-public": "token-1", "ep-private": "token-2"},
	})
	detail := func(id string) episodeDetailJSON {
		t.Helper()
		rec := serve(guests, handler, cookie, httptest.NewRequest(http.MethodGet, "/api/episodes/"+id, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("detail %s status = %d, want 200", id, rec.Code)
		}
		var body episodeDetailJSON
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("decode detail %s: %v", id, err)
		}
		return body
	}

	if got := detail("ep-public"); got.SharePath != "/share/token-1" {
		t.Fatalf("public share path = %q, want /share/token-1", got.SharePath)
	}
	if got := detail("ep-private"); got.SharePath != "" {
		t.Fatalf("private share path = %q, want empty", got.SharePath)
	}

	bare := NewEpisodes(stack.svc)
	rec := serve(guests, bare, cookie, httptest.NewRequest(http.MethodGet, "/api/episodes/ep-public", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("bare detail status = %d, want 200", rec.Code)
	}
	var decoded episodeDetailJSON
	if err := json.NewDecoder(rec.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode bare detail: %v", err)
	}
	if decoded.SharePath != "" {
		t.Fatalf("bare share path = %q, want empty with no token reader", decoded.SharePath)
	}
}

// TestEpisodeDetailServesWordsStemAndRender stores edit words, both
// stems, and two renders, and requires the detail to return word
// seconds, the user stem address, and the newest render address. A
// second episode with only a host stem and no render omits the render
// address and serves the host stem. Rendered words ride beside the
// render they came from, never beside a newer render, and never beside an
// episode with no render.
func TestEpisodeDetailServesWordsStemAndRender(t *testing.T) {
	t.Parallel()
	db, guests := openDiary(t)
	cookie, owner := mintGuest(t, guests)
	seedEpisodeRow(t, db, "ep-1", owner.ID, 1, "draft")
	seedProposalRow(t, db, "cut-1", owner.ID, "ep-1")
	seedWordRow(t, db, "word-late", owner.ID, "ep-1", "Later", "edit", 2000, 2500)
	seedWordRow(t, db, "word-early", owner.ID, "ep-1", "Hello", "edit", 400, 900)
	seedStemRow(t, db, "stem-host", owner.ID, "ep-1", "host-blob", "host")
	seedStemRow(t, db, "stem-user", owner.ID, "ep-1", "user-blob", "user")
	seedRenderRow(t, db, "render-old", owner.ID, "ep-1", "opus-old")
	seedRenderRow(t, db, "render-new", owner.ID, "ep-1", "opus-new")
	if err := analysis.ReplaceWords(t.Context(), db.Writer(), owner.ID, "ep-1", "render-new",
		[]analysis.Word{{Text: "Nope", StartMs: 100, EndMs: 200}}); err != nil {
		t.Fatalf("seed rendered words: %v", err)
	}

	seedEpisodeRow(t, db, "ep-2", owner.ID, 2, "ready")
	seedStemRow(t, db, "stem-host-only", owner.ID, "ep-2", "host-only", "host")
	seedWordRow(t, db, "word-stale", owner.ID, "ep-2", "Stale", "rendered", 0, 300)

	handler := NewEpisodes(newEpisodeService(t, db, nil).svc)
	detail := func(id string) episodeDetailJSON {
		t.Helper()
		rec := serve(guests, handler, cookie, httptest.NewRequest(http.MethodGet, "/api/episodes/"+id, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("detail %s status = %d, want 200", id, rec.Code)
		}
		var body episodeDetailJSON
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("decode detail %s: %v", id, err)
		}
		return body
	}

	got := detail("ep-1")
	if len(got.Proposals) != 1 || got.Proposals[0].ID != "cut-1" {
		t.Fatalf("proposals = %+v, want the stored cut", got.Proposals)
	}
	if len(got.Words) != 2 {
		t.Fatalf("words = %+v, want the two edit words", got.Words)
	}
	if got.Words[0].Text != "Hello" || !secondsNear(got.Words[0].Start, 0.4) || !secondsNear(got.Words[0].End, 0.9) {
		t.Fatalf("first word = %+v, want Hello from 0.4 to 0.9", got.Words[0])
	}
	if got.Words[1].Text != "Later" || !secondsNear(got.Words[1].Start, 2) || !secondsNear(got.Words[1].End, 2.5) {
		t.Fatalf("second word = %+v, want Later from 2 to 2.5", got.Words[1])
	}
	if got.AudioURL != "/media/user-blob" {
		t.Fatalf("audio address = %q, want the user stem", got.AudioURL)
	}
	if got.RenderAudioURL != "/media/opus-new" {
		t.Fatalf("render address = %q, want the newest render", got.RenderAudioURL)
	}
	if len(got.RenderWords) != 1 || got.RenderWords[0].Text != "Nope" ||
		!secondsNear(got.RenderWords[0].Start, 0.1) || !secondsNear(got.RenderWords[0].End, 0.2) {
		t.Fatalf("render words = %+v, want Nope from 0.1 to 0.2", got.RenderWords)
	}

	seedRenderRow(t, db, "render-newest", owner.ID, "ep-1", "opus-newest")
	rerendered := detail("ep-1")
	if rerendered.RenderAudioURL != "/media/opus-newest" {
		t.Fatalf("render address = %q, want the newest render", rerendered.RenderAudioURL)
	}
	if rerendered.RenderWords == nil || len(rerendered.RenderWords) != 0 {
		t.Fatalf("render words = %+v, want none under a render analysis never read", rerendered.RenderWords)
	}

	bare := detail("ep-2")
	if len(bare.Words) != 0 {
		t.Fatalf("words = %+v, want none", bare.Words)
	}
	if bare.AudioURL != "/media/host-only" {
		t.Fatalf("audio address = %q, want the host stem", bare.AudioURL)
	}
	if bare.RenderAudioURL != "" {
		t.Fatalf("render address = %q, want empty", bare.RenderAudioURL)
	}
	if bare.RenderWords == nil || len(bare.RenderWords) != 0 {
		t.Fatalf("render words = %+v, want an empty list with no render", bare.RenderWords)
	}
}
