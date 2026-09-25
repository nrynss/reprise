package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nrynss/keel/cost"
	"github.com/nrynss/keel/gate"
	"github.com/nrynss/keel/id"
	"github.com/nrynss/keel/job"
	"github.com/nrynss/keel/mediastore"
	"github.com/nrynss/reprise/internal/cover"
	"github.com/nrynss/reprise/internal/export"
	"github.com/nrynss/reprise/internal/identity"
	"github.com/nrynss/reprise/internal/settings"
)

// exportCoverFail draws nothing. The cover pass stores its deterministic
// fallback on this path, so the bundle test needs no model.
type exportCoverFail struct{}

// GenerateImage refuses every call, so the pass stores its fallback.
func (exportCoverFail) GenerateImage(context.Context, string, cover.ImageRequest) (cover.ImageAnswer, error) {
	return cover.ImageAnswer{}, errExportCover
}

// errExportCover is the answer the still cover model gives every call,
// so the cover pass stores its deterministic panel.
var errExportCover = errors.New("reprise: export test draws no art")

// exportCoverBudget books nothing, because the failing model calls
// nothing paid.
type exportCoverBudget struct{}

// Reserve books nothing.
func (exportCoverBudget) Reserve(cost.Price) error { return nil }

// Settle books nothing.
func (exportCoverBudget) Settle(cost.Price, cost.Price) error { return nil }

// Release frees nothing.
func (exportCoverBudget) Release(cost.Price) {}

// exportWorld carries the mounted export route over fixture stores.
type exportWorld struct {
	mux      *http.ServeMux
	fx       *wireFixture
	guests   *identity.Service
	coverDir string
}

// openExportWorld builds the diary, the identity service, and the
// export hook the way the boot does. The kinds hook builds the service
// the mount hook serves, so the pins hold for the binary too. The
// holder is reset after the test, so no case inherits another case's
// service.
func openExportWorld(t *testing.T) *exportWorld {
	t.Helper()
	fx := openWireFixture(t)
	guests, err := identity.New(t.Context(), identity.Config{DB: fx.db, SigningKey: "export-test-signing-key"})
	if err != nil {
		t.Fatalf("open identity: %v", err)
	}
	loaded := settings.Settings{MediaDir: fx.pipe.mediaDir}
	kinds, err := exportKinds(kindWiring{DB: fx.db, Media: fx.media, Settings: loaded})
	if err != nil {
		t.Fatalf("export kinds: %v", err)
	}
	t.Cleanup(func() { holdExportServices(nil) })
	if _, ok := kinds[export.KindName]; !ok {
		t.Fatalf("kinds miss the export kind %q", export.KindName)
	}
	world := &exportWorld{mux: http.NewServeMux(), fx: fx, guests: guests, coverDir: filepath.Join(loaded.MediaDir, "covers")}
	spendGate, err := gate.New(gate.Config{})
	if err != nil {
		t.Fatalf("open spend gate: %v", err)
	}
	rule := gate.Rule{
		Name:      "export-test",
		PerClient: gate.Limit{Burst: 64, Every: time.Second},
		Global:    gate.Limit{Burst: 64, Every: time.Second},
	}
	if err := mountExport(t.Context(), routeWiring{
		Mux:      world.mux,
		Gate:     spendGate,
		Outer:    rule,
		Guests:   guests.Middleware,
		DB:       fx.db,
		Media:    fx.media,
		Settings: loaded,
	}); err != nil {
		t.Fatalf("mount export: %v", err)
	}
	return world
}

// genExportAAC renders a fixed length AAC file and returns its bytes.
// The tone is generated, so no person and no microphone is involved.
func genExportAAC(t *testing.T, secs int) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "render.m4a")
	cmd := exec.CommandContext(t.Context(), "ffmpeg", "-hide_banner", "-y",
		"-f", "lavfi", "-i", "sine=frequency=440:duration="+strconv.Itoa(secs),
		"-c:a", "aac", "-b:a", "128k", path)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("render aac: %v: %s", err, stderr.String())
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read aac: %v", err)
	}
	return raw
}

// exportSeed names the rows one mounted episode owns.
type exportSeed struct {
	episode string
	blob    string
}

// seedExportEpisode writes one ready episode with a real AAC render,
// rendered words, analysis chapters with notes, and a fallback cover.
// Every row carries the owner, so a second owner seeded beside it
// shares nothing but the blob bytes when blobID names one. A blank
// blobID persists fresh audio for the owner.
func seedExportEpisode(t *testing.T, world *exportWorld, owner string, number int64, title, notes string, seeded bool, blobID string) exportSeed {
	t.Helper()
	ctx := t.Context()
	episodeID, err := id.New()
	if err != nil {
		t.Fatalf("mint episode id: %v", err)
	}
	seededFlag := 0
	if seeded {
		seededFlag = 1
	}
	if _, err := world.fx.db.Writer().ExecContext(ctx, `INSERT INTO episodes
		(id, owner_id, number, title, state, visibility, share_token, seeded)
		VALUES (?, ?, ?, ?, 'ready', 'private', ?, ?)`,
		episodeID, owner, number, title, "token-"+episodeID, seededFlag); err != nil {
		t.Fatalf("seed episode: %v", err)
	}
	if blobID == "" {
		persisted, err := world.fx.media.Persist(ctx, bytes.NewReader(genExportAAC(t, 36)), mediastore.Put{
			ContentType: "audio/mp4",
			Owner:       owner,
			Group:       episodeID,
			Visibility:  mediastore.Private,
		})
		if err != nil {
			t.Fatalf("persist aac: %v", err)
		}
		blobID = persisted
	}
	opus, err := world.fx.media.Persist(ctx, strings.NewReader("opus "+episodeID), mediastore.Put{
		ContentType: "audio/ogg",
		Owner:       owner,
		Group:       episodeID,
		Visibility:  mediastore.Private,
	})
	if err != nil {
		t.Fatalf("persist opus: %v", err)
	}
	renderID, err := id.New()
	if err != nil {
		t.Fatalf("mint render id: %v", err)
	}
	if _, err := world.fx.db.Writer().ExecContext(ctx, `INSERT INTO renders
		(id, owner_id, episode_id, input_hash, opus_media_id, aac_media_id, loudness)
		VALUES (?, ?, ?, 'hash', ?, ?, -16)`, renderID, owner, episodeID, opus, blobID); err != nil {
		t.Fatalf("seed render: %v", err)
	}
	pool := []string{"the", "host", "asks", "again", "next", "week", "about", "the", "thing", "you", "said"}
	i := 0
	for start := int64(0); start+500 <= 35000; start += 500 {
		wordID, err := id.New()
		if err != nil {
			t.Fatalf("mint word id: %v", err)
		}
		if _, err := world.fx.db.Writer().ExecContext(ctx,
			`INSERT INTO words (id, owner_id, episode_id, text, start_ms, end_ms, source)
			 VALUES (?, ?, ?, ?, ?, ?, 'rendered')`,
			wordID, owner, episodeID, pool[i%len(pool)], start, start+500); err != nil {
			t.Fatalf("seed word: %v", err)
		}
		i++
	}
	analysisID, err := id.New()
	if err != nil {
		t.Fatalf("mint analysis id: %v", err)
	}
	chapters := `[{"title":"Opening","start_ms":0},{"title":"The middle","start_ms":12000},{"title":"The close","start_ms":24000}]`
	summary, err := json.Marshal(map[string]any{"headline": title, "bullets": []string{}, "block_summary": notes})
	if err != nil {
		t.Fatalf("encode summary: %v", err)
	}
	if _, err := world.fx.db.Writer().ExecContext(ctx, `INSERT INTO analyses
		(id, owner_id, episode_id, transcript_id, chapters, summary, entities, key_phrases)
		VALUES (?, ?, ?, ?, ?, ?, '[]', '[]')`,
		analysisID, owner, episodeID, "tx-"+episodeID, chapters, string(summary)); err != nil {
		t.Fatalf("seed analysis: %v", err)
	}
	if _, err := cover.Run(ctx, cover.Config{
		DB:        world.fx.db.Writer(),
		Model:     exportCoverFail{},
		Budgets:   exportCoverBudget{},
		ModelID:   "export-test-model",
		OwnerID:   owner,
		EpisodeID: episodeID,
		Dir:       world.coverDir,
		SaveRaw:   func(context.Context, []byte) error { return nil },
	}); err != nil {
		t.Fatalf("seed cover: %v", err)
	}
	return exportSeed{episode: episodeID, blob: blobID}
}

// serveExport runs one mounted request with an optional cookie and
// returns the recorder.
func serveExport(world *exportWorld, method, path string, cookie *http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	world.mux.ServeHTTP(rec, req)
	return rec
}

// exportCode reads the envelope code one refusal carries, or empty
// when the body drifts.
func exportCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode refusal: %v", err)
	}
	return body.Error.Code
}

// unzipExport reads every bundle entry into memory in zip order.
func unzipExport(t *testing.T, raw []byte) ([]string, map[string][]byte) {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatalf("open bundle zip: %v", err)
	}
	var order []string
	files := map[string][]byte{}
	for _, entry := range reader.File {
		body, err := entry.Open()
		if err != nil {
			t.Fatalf("open %s: %v", entry.Name, err)
		}
		content, err := io.ReadAll(body)
		closeErr := body.Close()
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name, err)
		}
		if closeErr != nil {
			t.Fatalf("close %s: %v", entry.Name, closeErr)
		}
		order = append(order, entry.Name)
		files[entry.Name] = content
	}
	return order, files
}

// probeExportStream reads one ffprobe field for one stream selector,
// independent of the code under test.
func probeExportStream(t *testing.T, path, selector, field string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "ffprobe", "-hide_banner", "-v", "error",
		"-select_streams", selector, "-show_entries", "stream="+field,
		"-of", "default=noprint_wrappers=1:nokey=1", path)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("probe %s %s: %v", selector, field, err)
	}
	return strings.Join(strings.Fields(strings.TrimSpace(string(out))), ",")
}

// TestExportKindsRegisterBundle pins the kinds hook. The export kind
// reaches the runner under its own name, and a second registration of
// that name refuses through the existing merge. Bare wiring registers
// nothing, so hook tests call every hook without a database.
func TestExportKindsRegisterBundle(t *testing.T) {
	fx := openWireFixture(t)
	loaded := settings.Settings{MediaDir: fx.pipe.mediaDir}
	kinds, err := exportKinds(kindWiring{DB: fx.db, Media: fx.media, Settings: loaded})
	if err != nil {
		t.Fatalf("export kinds: %v", err)
	}
	t.Cleanup(func() { holdExportServices(nil) })
	if _, ok := kinds[export.KindName]; !ok {
		t.Fatalf("kinds miss the export kind %q", export.KindName)
	}
	if _, err := mergeKinds(map[string]job.Kind{export.KindName: {Limit: 1}}, kinds); !errors.Is(err, errDuplicateKind) {
		t.Fatalf("a repeated export kind merged without a refusal, err = %v", err)
	}
	empty, err := exportKinds(kindWiring{})
	if err != nil {
		t.Fatalf("bare kinds: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("bare kinds registered %d kinds, want none", len(empty))
	}
}

// TestExportServesBundleToOwner pins the owner round trip. A ready
// episode downloads audio, video, captions, cover, and a chapter
// description in one zip. ffprobe reads the audio and the video, and
// the headers keep the private bundle out of caches.
func TestExportServesBundleToOwner(t *testing.T) {
	world := openExportWorld(t)
	cookie, owner := finishGuest(t, world.guests)
	notes := "The host asks again next week about the thing you said."
	seed := seedExportEpisode(t, world, owner, 4, "A quiet take", notes, false, "")

	rec := serveExport(world, http.MethodGet, "/api/episodes/"+seed.episode+"/export", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("export status %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if contentType := rec.Header().Get("Content-Type"); contentType != "application/zip" {
		t.Fatalf("export type %q, want application/zip", contentType)
	}
	if disposition := rec.Header().Get("Content-Disposition"); !strings.Contains(disposition, "episode-4-bundle.zip") {
		t.Fatalf("export disposition %q names no episode bundle", disposition)
	}
	if cache := rec.Header().Get("Cache-Control"); cache != "no-store" {
		t.Fatalf("export cache %q, want no-store", cache)
	}
	order, files := unzipExport(t, rec.Body.Bytes())
	if len(order) != len(export.BundleOrder) {
		t.Fatalf("bundle holds %d files, want %d", len(order), len(export.BundleOrder))
	}
	for i, name := range export.BundleOrder {
		if order[i] != name {
			t.Fatalf("entry %d is %q, want %q", i+1, order[i], name)
		}
		if len(files[name]) == 0 {
			t.Fatalf("entry %s is empty", name)
		}
	}
	dir := t.TempDir()
	writeEntry := func(name, file string) string {
		path := filepath.Join(dir, file)
		if err := os.WriteFile(path, files[name], 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		return path
	}
	if codec := probeExportStream(t, writeEntry(export.FileAudio, "a.m4a"), "a:0", "codec_name"); codec != "aac" {
		t.Fatalf("bundle audio codec is %q, want aac", codec)
	}
	video := writeEntry(export.FileVideo, "v.mp4")
	if frame := probeExportStream(t, video, "v:0", "width,height"); frame != "1920,1080" {
		t.Fatalf("bundle video frame is %q, want 1920,1080", frame)
	}
	if codec := probeExportStream(t, video, "a:0", "codec_name"); codec != "aac" {
		t.Fatalf("bundle video audio codec is %q, want aac", codec)
	}
	if !strings.Contains(string(files[export.FileSRT]), "-->") {
		t.Fatalf("bundle srt carries no cue timing")
	}
	if !strings.HasPrefix(string(files[export.FileVTT]), "WEBVTT") {
		t.Fatalf("bundle vtt misses its header")
	}
	description := string(files[export.FileDescription])
	if !strings.HasPrefix(description, notes) {
		t.Fatalf("description does not open with the show notes: %q", description)
	}
	if !strings.Contains(description, "0:00 Opening") {
		t.Fatalf("description misses its chapter lines: %q", description)
	}
	if rec := serveExport(world, http.MethodPost, "/api/episodes/"+seed.episode+"/export", cookie); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("post export status %d, want 405", rec.Code)
	}
}

// TestExportRefusesStranger pins the owner scoping. A stranger and a
// signed-out visitor read the same 404 as a missing episode, and an
// unknown id reads 404 for the owner too.
func TestExportRefusesStranger(t *testing.T) {
	world := openExportWorld(t)
	cookie, owner := finishGuest(t, world.guests)
	seed := seedExportEpisode(t, world, owner, 1, "A quiet take", "Notes stay with the owner.", false, "")

	stranger, _ := finishGuest(t, world.guests)
	if rec := serveExport(world, http.MethodGet, "/api/episodes/"+seed.episode+"/export", stranger); rec.Code != http.StatusNotFound {
		t.Fatalf("stranger export status %d, want 404", rec.Code)
	} else if code := exportCode(t, rec); code != export.CodeNotFound {
		t.Fatalf("stranger export code %q, want not_found", code)
	}
	if rec := serveExport(world, http.MethodGet, "/api/episodes/"+seed.episode+"/export", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("signed-out export status %d, want 404", rec.Code)
	}
	if rec := serveExport(world, http.MethodGet, "/api/episodes/no-such-episode/export", cookie); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown export status %d, want 404", rec.Code)
	}
}

// TestExportRefusesUnfinished pins the ready gate. An episode that
// never finished refuses with the stable code, so the screen explains
// the wait instead of downloading half a bundle.
func TestExportRefusesUnfinished(t *testing.T) {
	world := openExportWorld(t)
	cookie, owner := finishGuest(t, world.guests)
	episodeID, err := id.New()
	if err != nil {
		t.Fatalf("mint episode id: %v", err)
	}
	if _, err := world.fx.db.Writer().ExecContext(t.Context(), `INSERT INTO episodes
		(id, owner_id, number, title, state, visibility, share_token, seeded)
		VALUES (?, ?, 2, 'A draft take', 'rendering', 'private', ?, 0)`,
		episodeID, owner, "token-"+episodeID); err != nil {
		t.Fatalf("seed draft: %v", err)
	}
	rec := serveExport(world, http.MethodGet, "/api/episodes/"+episodeID+"/export", cookie)
	if rec.Code != http.StatusConflict {
		t.Fatalf("draft export status %d, want 409", rec.Code)
	}
	if code := exportCode(t, rec); code != export.CodeNotReady {
		t.Fatalf("draft export code %q, want export_not_ready", code)
	}
}

// TestExportNamesOnlyRequestingOwner pins the seeded copy isolation.
// Two owners share catalog blob bytes, and each bundle still names its
// own episode notes and nothing of the other's.
func TestExportNamesOnlyRequestingOwner(t *testing.T) {
	world := openExportWorld(t)
	cookieA, ownerA := finishGuest(t, world.guests)
	cookieB, ownerB := finishGuest(t, world.guests)
	seedA := seedExportEpisode(t, world, ownerA, 1, "Catalog take", "Catalog notes stay with the catalog owner.", false, "")
	seedB := seedExportEpisode(t, world, ownerB, 1, "Seeded copy", "Seeded notes stay with the seeded owner.", true, seedA.blob)

	rec := serveExport(world, http.MethodGet, "/api/episodes/"+seedB.episode+"/export", cookieB)
	if rec.Code != http.StatusOK {
		t.Fatalf("seeded export status %d, want 200: %s", rec.Code, rec.Body.String())
	}
	_, files := unzipExport(t, rec.Body.Bytes())
	description := string(files[export.FileDescription])
	if !strings.Contains(description, "Seeded notes stay with the seeded owner.") {
		t.Fatalf("seeded description misses its own notes: %q", description)
	}
	if strings.Contains(description, "Catalog notes") {
		t.Fatalf("seeded description leaks the other owner: %q", description)
	}
	if rec := serveExport(world, http.MethodGet, "/api/episodes/"+seedA.episode+"/export", cookieB); rec.Code != http.StatusNotFound {
		t.Fatalf("cross owner export status %d, want 404", rec.Code)
	}
	if rec := serveExport(world, http.MethodGet, "/api/episodes/"+seedA.episode+"/export", cookieA); rec.Code != http.StatusOK {
		t.Fatalf("catalog export status %d, want 200: %s", rec.Code, rec.Body.String())
	}
}
