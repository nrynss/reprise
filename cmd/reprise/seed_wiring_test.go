package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nrynss/reprise/internal/host"
	"github.com/nrynss/reprise/internal/identity"
)

// writeSeasonFile writes one catalog file with its audio sibling into the
// boot season directory, so the boot sync imports it.
func writeSeasonFile(t *testing.T, dir, key, body string, audio []byte) {
	t.Helper()
	season := filepath.Join(dir, "season")
	if err := os.MkdirAll(season, 0o755); err != nil {
		t.Fatalf("make season dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(season, key+".json"), []byte(body), 0o600); err != nil {
		t.Fatalf("write catalog file: %v", err)
	}
	if audio != nil {
		name := key + ".opus"
		if strings.Contains(body, `"audio"`) {
			name = key + ".opus"
		}
		if err := os.WriteFile(filepath.Join(season, name), audio, 0o600); err != nil {
			t.Fatalf("write catalog audio: %v", err)
		}
	}
}

// oneEpisodeCatalog is a single finished episode with two names and a
// planted opening for the next one.
func oneEpisodeCatalog() string {
	return `{"title": "The dreaded conversation", "number": 1, "audio": "dread.opus",` +
		` "words": [{"text": "Maya", "start_ms": 0, "end_ms": 120},` +
		` {"text": "called", "start_ms": 120, "end_ms": 240}],` +
		` "mentions": [{"kind": "person_name", "word_offset": 0, "quote": "Maya"},` +
		` {"kind": "person_name", "word_offset": 1, "quote": "Jonas"}],` +
		` "planted_quote": "Maya"}`
}

// welcomeAnswer decodes one first visit read.
type welcomeAnswer struct {
	Mode     string `json:"mode"`
	Episodes int    `json:"episodes"`
	Teaser   *struct {
		EpisodeNumber int    `json:"episode_number"`
		Title         string `json:"title"`
		LineA         string `json:"line_a"`
		LineB         string `json:"line_b"`
		AudioURL      string `json:"audio_url"`
	} `json:"teaser"`
}

// readWelcome calls the first visit route and returns its answer with the
// cookies the guest middleware set.
func readWelcome(t *testing.T, mux *http.ServeMux, cookie string) (welcomeAnswer, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/welcome", nil)
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("welcome status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var out welcomeAnswer
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode welcome: %v", err)
	}
	jar := rec.Header().Get("Set-Cookie")
	if jar != "" {
		cookie = strings.SplitN(jar, ";", 2)[0]
	}
	return out, cookie
}

// TestSeedWelcomeCopiesOneEpisodeOnce boots the wired binary over a one
// episode catalog and proves a new guest gallery holds that copy once,
// flagged, with its mention feeding the host opening.
func TestSeedWelcomeCopiesOneEpisodeOnce(t *testing.T) {
	dir := t.TempDir()
	loaded := bootSettings(t, dir)
	writeSeasonFile(t, loaded.DataDir, "dread", oneEpisodeCatalog(), []byte("fake opus mix"))
	ctx, stop := context.WithCancel(t.Context())
	defer stop()
	mux := http.NewServeMux()
	if _, err := wireAPI(ctx, mux, loaded, bootPlan{features: features()}); err != nil {
		t.Fatalf("boot: %v", err)
	}
	first, cookie := readWelcome(t, mux, "")
	if first.Mode != "seeded" || first.Episodes != 1 {
		t.Fatalf("first welcome = %+v, want one seeded episode", first)
	}
	if first.Teaser == nil || first.Teaser.EpisodeNumber != 1 || first.Teaser.Title == "" {
		t.Fatalf("first teaser = %+v, want episode 1 with a title", first.Teaser)
	}
	if first.Teaser.LineA != "Maya" || first.Teaser.AudioURL == "" {
		t.Fatalf("first teaser = %+v, want the Maya quote with render audio", first.Teaser)
	}
	if cookie == "" {
		t.Fatal("welcome read sets no guest cookie")
	}
	second, cookie := readWelcome(t, mux, cookie)
	if second.Episodes != 1 {
		t.Fatalf("second welcome episodes = %d, want the single copy", second.Episodes)
	}
	db := openBootDB(t, loaded)
	var guest string
	if err := db.Reader().QueryRowContext(t.Context(),
		`SELECT id FROM users WHERE kind = ?`, identity.KindGuest).Scan(&guest); err != nil {
		t.Fatalf("read guest: %v", err)
	}
	var copies, seeded, receipts int
	if err := db.Reader().QueryRowContext(t.Context(),
		`SELECT COUNT(*) FROM episodes WHERE owner_id = ?`, guest).Scan(&copies); err != nil {
		t.Fatalf("count copies: %v", err)
	}
	if err := db.Reader().QueryRowContext(t.Context(),
		`SELECT COUNT(*) FROM episodes WHERE owner_id = ? AND seeded = 1`, guest).Scan(&seeded); err != nil {
		t.Fatalf("count seeded: %v", err)
	}
	if err := db.Reader().QueryRowContext(t.Context(),
		`SELECT COUNT(*) FROM seed_receipts WHERE user_id = ?`, guest).Scan(&receipts); err != nil {
		t.Fatalf("count receipts: %v", err)
	}
	if copies != 1 || seeded != 1 || receipts != 1 {
		t.Fatalf("copies = %d, seeded = %d, receipts = %d, want one each", copies, seeded, receipts)
	}
	var renderWords int
	if err := db.Reader().QueryRowContext(t.Context(),
		`SELECT COUNT(*) FROM words WHERE owner_id = ? AND source = 'rendered'`, guest).Scan(&renderWords); err != nil {
		t.Fatalf("count rendered words: %v", err)
	}
	if renderWords != 2 {
		t.Fatalf("rendered words = %d, want the two copied words", renderWords)
	}
	var link string
	if err := db.Reader().QueryRowContext(t.Context(),
		`SELECT render_id FROM rendered_sources WHERE episode_id =
		 (SELECT id FROM episodes WHERE owner_id = ? LIMIT 1)`, guest).Scan(&link); err != nil {
		t.Fatalf("read copy link: %v", err)
	}
	if link == "" {
		t.Fatal("copy stores rendered words with no render link")
	}
	cfg, err := host.Load(t.Context(), db.Writer(), guest)
	if err != nil {
		t.Fatalf("load host config: %v", err)
	}
	if !strings.Contains(cfg.Greeting, "Maya") {
		t.Fatalf("greeting = %q, want the copied Maya mention", cfg.Greeting)
	}
	list := httptest.NewRequest(http.MethodGet, "/api/episodes", nil)
	list.Header.Set("Cookie", cookie)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, list)
	if rec.Code != http.StatusOK {
		t.Fatalf("episode list status = %d, want %d: %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var season struct {
		Episodes []struct {
			Title string `json:"title"`
		} `json:"episodes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &season); err != nil {
		t.Fatalf("decode season: %v", err)
	}
	if len(season.Episodes) != 1 || season.Episodes[0].Title != "The dreaded conversation" {
		t.Fatalf("gallery holds %+v, want the one copied episode", season.Episodes)
	}
}

// TestSeedBootRefusesBadCatalog pins the boot refusal. A catalog file
// that fails validation stops the boot with its key in the error.
func TestSeedBootRefusesBadCatalog(t *testing.T) {
	dir := t.TempDir()
	loaded := bootSettings(t, dir)
	writeSeasonFile(t, loaded.DataDir, "broken", `{"number": 1}`, nil)
	ctx, stop := context.WithCancel(t.Context())
	defer stop()
	_, err := wireAPI(ctx, http.NewServeMux(), loaded, bootPlan{features: features()})
	if err == nil {
		t.Fatal("boot accepts the catalog file it must refuse")
	}
	if !strings.Contains(err.Error(), "broken") {
		t.Fatalf("boot error = %v, want the file key", err)
	}
}

// TestSeedWelcomeEmptyCatalog pins the empty read. No files means the
// mode is empty with no teaser, so the screen offers only the record
// button.
func TestSeedWelcomeEmptyCatalog(t *testing.T) {
	loaded := bootSettings(t, t.TempDir())
	ctx, stop := context.WithCancel(t.Context())
	defer stop()
	mux := http.NewServeMux()
	if _, err := wireAPI(ctx, mux, loaded, bootPlan{features: features()}); err != nil {
		t.Fatalf("boot: %v", err)
	}
	out, _ := readWelcome(t, mux, "")
	if out.Mode != "empty" || out.Episodes != 0 || out.Teaser != nil {
		t.Fatalf("empty welcome = %+v, want no teaser", out)
	}
}
