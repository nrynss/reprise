package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nrynss/keel/sqlite"
)

// seedSpeakerWord writes one word with its speaker and fails the test on
// error. An empty speaker models a row stored before the speaker column.
func seedSpeakerWord(t *testing.T, db *sqlite.DB, id, owner, episodeID, text, source, speaker string, startMs, endMs int64) {
	t.Helper()
	if _, err := db.Writer().ExecContext(t.Context(),
		`INSERT INTO words (id, owner_id, episode_id, text, start_ms, end_ms, source, speaker)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id, owner, episodeID, text, startMs, endMs, source, speaker); err != nil {
		t.Fatalf("seed word %s: %v", id, err)
	}
}

// seedRenderedSource names the render the stored rendered words came from
// and fails the test on error.
func seedRenderedSource(t *testing.T, db *sqlite.DB, episodeID, renderID string) {
	t.Helper()
	if _, err := db.Writer().ExecContext(t.Context(),
		`INSERT INTO rendered_sources (episode_id, render_id) VALUES (?, ?)`,
		episodeID, renderID); err != nil {
		t.Fatalf("seed rendered source: %v", err)
	}
}

// TestEpisodeDetailForwardsWordSpeakers stores one host word, one guest
// word, and one legacy word with no speaker, then reads the episode detail
// endpoint. It requires the typed words to name host, guest, and empty in
// start order, with the same trio on the rendered words. It also requires
// every raw word object on both lists to carry a string speaker key, so a
// dropped forward fails this test instead of regressing silently.
func TestEpisodeDetailForwardsWordSpeakers(t *testing.T) {
	t.Parallel()
	db, guests := openDiary(t)
	cookie, owner := mintGuest(t, guests)
	seedEpisodeRow(t, db, "ep-1", owner.ID, 1, "draft")
	seedSpeakerWord(t, db, "word-host", owner.ID, "ep-1", "Hello", "edit", "host", 400, 900)
	seedSpeakerWord(t, db, "word-guest", owner.ID, "ep-1", "Hi", "edit", "user", 1000, 1300)
	seedSpeakerWord(t, db, "word-legacy", owner.ID, "ep-1", "Old", "edit", "", 2000, 2500)
	seedRenderRow(t, db, "render-new", owner.ID, "ep-1", "opus-new")
	seedSpeakerWord(t, db, "word-render-host", owner.ID, "ep-1", "Cast", "rendered", "host", 100, 200)
	seedSpeakerWord(t, db, "word-render-legacy", owner.ID, "ep-1", "Dust", "rendered", "", 300, 400)
	seedRenderedSource(t, db, "ep-1", "render-new")

	handler := NewEpisodes(newEpisodeService(t, db, nil).svc)
	rec := serve(guests, handler, cookie, httptest.NewRequest(http.MethodGet, "/api/episodes/ep-1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("detail status = %d, want 200", rec.Code)
	}
	raw, err := io.ReadAll(rec.Body)
	if err != nil {
		t.Fatalf("read detail body: %v", err)
	}
	var body episodeDetailJSON
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	wantSpeakers := []string{"host", "user", ""}
	if len(body.Words) != len(wantSpeakers) {
		t.Fatalf("words = %+v, want three words in start order", body.Words)
	}
	for i, want := range wantSpeakers {
		if body.Words[i].Speaker != want {
			t.Fatalf("word %d speaker = %q, want %q", i, body.Words[i].Speaker, want)
		}
	}
	wantRendered := []string{"host", ""}
	if len(body.RenderWords) != len(wantRendered) {
		t.Fatalf("render words = %+v, want two rendered words in start order", body.RenderWords)
	}
	for i, want := range wantRendered {
		if body.RenderWords[i].Speaker != want {
			t.Fatalf("render word %d speaker = %q, want %q", i, body.RenderWords[i].Speaker, want)
		}
	}

	var wireBody struct {
		Words       []map[string]any `json:"words"`
		RenderWords []map[string]any `json:"render_words"`
	}
	if err := json.Unmarshal(raw, &wireBody); err != nil {
		t.Fatalf("decode raw detail: %v", err)
	}
	checkKeys := func(name string, words []map[string]any, want []string) {
		t.Helper()
		if len(words) != len(want) {
			t.Fatalf("%s holds %d words, want %d", name, len(words), len(want))
		}
		for i, word := range words {
			got, ok := word["speaker"]
			if !ok {
				t.Fatalf("%s word %d carries no speaker key", name, i)
			}
			text, ok := got.(string)
			if !ok {
				t.Fatalf("%s word %d speaker is not a string", name, i)
			}
			if text != want[i] {
				t.Fatalf("%s word %d speaker = %q, want %q", name, i, text, want[i])
			}
		}
	}
	checkKeys("words", wireBody.Words, wantSpeakers)
	checkKeys("render_words", wireBody.RenderWords, wantRendered)
}
