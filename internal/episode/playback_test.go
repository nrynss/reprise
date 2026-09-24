package episode_test

import (
	"errors"
	"math"
	"testing"

	"github.com/nrynss/reprise/internal/episode"
)

// TestPlaybackReadsStoredRows requires edit words in start order with
// times in seconds, the user stem ahead of the host stem, and the newest
// render. A host-only episode with no render returns that stem and an
// empty render id.
func TestPlaybackReadsStoredRows(t *testing.T) {
	t.Parallel()
	db := openDatabase(t)
	plantEpisode(t, db, "ep-1", 1, episode.StateDraft)
	if _, err := db.Writer().ExecContext(t.Context(),
		`INSERT INTO words (id, owner_id, episode_id, text, start_ms, end_ms, source) VALUES
		 ('word-late', 'owner-1', 'ep-1', 'Later', 2000, 2500, 'edit'),
		 ('word-early', 'owner-1', 'ep-1', 'Hello', 400, 900, 'edit'),
		 ('word-other', 'owner-1', 'ep-1', 'Nope', 100, 200, 'rendered')`); err != nil {
		t.Fatalf("seed words: %v", err)
	}
	if _, err := db.Writer().ExecContext(t.Context(),
		`INSERT INTO stems (id, owner_id, episode_id, media_id, role, sample_rate, start_offset_ms) VALUES
		 ('stem-host', 'owner-1', 'ep-1', 'host-blob', 'host', 48000, 0),
		 ('stem-user', 'owner-1', 'ep-1', 'user-blob', 'user', 48000, 0)`); err != nil {
		t.Fatalf("seed stems: %v", err)
	}
	if _, err := db.Writer().ExecContext(t.Context(),
		`INSERT INTO renders (id, owner_id, episode_id, input_hash, opus_media_id, aac_media_id, loudness) VALUES
		 ('render-old', 'owner-1', 'ep-1', 'hash-old', 'opus-old', 'aac-old', -16),
		 ('render-new', 'owner-1', 'ep-1', 'hash-new', 'opus-new', 'aac-new', -14)`); err != nil {
		t.Fatalf("seed renders: %v", err)
	}
	svc, err := episode.NewService(episode.Config{DB: db})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	words, err := svc.EditWords(t.Context(), "owner-1", "ep-1")
	if err != nil {
		t.Fatalf("edit words: %v", err)
	}
	if len(words) != 2 || words[0].Text != "Hello" || math.Abs(words[0].Start-0.4) > 0.000001 || math.Abs(words[0].End-0.9) > 0.000001 {
		t.Fatalf("words = %+v, want Hello then Later in seconds", words)
	}
	if words[1].Text != "Later" || words[1].Start != 2 || words[1].End != 2.5 {
		t.Fatalf("second word = %+v, want Later from 2 to 2.5", words[1])
	}
	stem, err := svc.StemMediaID(t.Context(), "owner-1", "ep-1")
	if err != nil {
		t.Fatalf("stem: %v", err)
	}
	if stem != "user-blob" {
		t.Fatalf("stem = %q, want the user stem", stem)
	}
	render, err := svc.RenderMediaID(t.Context(), "owner-1", "ep-1")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if render != "opus-new" {
		t.Fatalf("render = %q, want the newest render", render)
	}

	plantEpisode(t, db, "ep-2", 2, episode.StateReady)
	if _, err := db.Writer().ExecContext(t.Context(),
		`INSERT INTO stems (id, owner_id, episode_id, media_id, role, sample_rate, start_offset_ms)
		 VALUES ('stem-host-only', 'owner-1', 'ep-2', 'host-only', 'host', 48000, 0)`); err != nil {
		t.Fatalf("seed host stem: %v", err)
	}
	host, err := svc.StemMediaID(t.Context(), "owner-1", "ep-2")
	if err != nil {
		t.Fatalf("host stem: %v", err)
	}
	if host != "host-only" {
		t.Fatalf("stem = %q, want the host stem", host)
	}
	missing, err := svc.RenderMediaID(t.Context(), "owner-1", "ep-2")
	if err != nil {
		t.Fatalf("missing render: %v", err)
	}
	if missing != "" {
		t.Fatalf("render = %q, want empty", missing)
	}
	if _, err := svc.EditWords(t.Context(), "owner-2", "ep-1"); !errors.Is(err, episode.ErrNotFound) {
		t.Fatalf("foreign words error = %v, want ErrNotFound", err)
	}
	if _, err := svc.EditWords(t.Context(), "", "ep-1"); !errors.Is(err, episode.ErrInvalid) {
		t.Fatalf("empty owner error = %v, want ErrInvalid", err)
	}
}

// TestRenderedWordsReadTheRenderClock requires the rendered source alone,
// in start order with times in seconds, and ErrNotFound for a foreign
// owner. An episode with no rendered words returns an empty slice.
func TestRenderedWordsReadTheRenderClock(t *testing.T) {
	t.Parallel()
	db := openDatabase(t)
	plantEpisode(t, db, "ep-1", 1, episode.StateReady)
	plantEpisode(t, db, "ep-2", 2, episode.StateDraft)
	if _, err := db.Writer().ExecContext(t.Context(),
		`INSERT INTO words (id, owner_id, episode_id, text, start_ms, end_ms, source) VALUES
		 ('word-edit', 'owner-1', 'ep-1', 'Raw', 0, 500, 'edit'),
		 ('word-late', 'owner-1', 'ep-1', 'Two', 1000, 1600, 'rendered'),
		 ('word-early', 'owner-1', 'ep-1', 'One', 0, 900, 'rendered')`); err != nil {
		t.Fatalf("seed words: %v", err)
	}
	svc, err := episode.NewService(episode.Config{DB: db})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	words, err := svc.RenderedWords(t.Context(), "owner-1", "ep-1")
	if err != nil {
		t.Fatalf("rendered words: %v", err)
	}
	if len(words) != 2 || words[0].Text != "One" || words[1].Text != "Two" ||
		math.Abs(words[1].Start-1) > 0.000001 || math.Abs(words[1].End-1.6) > 0.000001 {
		t.Fatalf("words = %+v, want One then Two in seconds", words)
	}
	none, err := svc.RenderedWords(t.Context(), "owner-1", "ep-2")
	if err != nil {
		t.Fatalf("empty rendered words: %v", err)
	}
	if none == nil || len(none) != 0 {
		t.Fatalf("words = %+v, want an empty slice", none)
	}
	if _, err := svc.RenderedWords(t.Context(), "owner-2", "ep-1"); !errors.Is(err, episode.ErrNotFound) {
		t.Fatalf("foreign words error = %v, want ErrNotFound", err)
	}
}
