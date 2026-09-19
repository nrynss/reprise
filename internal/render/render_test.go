package render_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"

	"github.com/nrynss/keel/job"
	"github.com/nrynss/keel/mediastore"
	mediasqlitestore "github.com/nrynss/keel/mediastore/sqlitestore"
	"github.com/nrynss/keel/sqlite"
	"github.com/nrynss/reprise/internal/editorial"
	"github.com/nrynss/reprise/internal/render"
	"github.com/nrynss/reprise/internal/store"
)

func openDiary(t *testing.T) (*sqlite.DB, *sql.DB) {
	t.Helper()
	db, err := sqlite.Open(t.Context(), sqlite.Config{
		Path:   filepath.Join(t.TempDir(), "diary.db"),
		Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := store.Open(t.Context(), db); err != nil {
		t.Fatalf("open store: %v", err)
	}
	return db, db.Writer()
}

func openMedia(t *testing.T, db *sqlite.DB) (render.Media, string) {
	t.Helper()
	ctx := t.Context()
	index, err := mediasqlitestore.Open(ctx, mediasqlitestore.Config{DB: db})
	if err != nil {
		t.Fatalf("open media index: %v", err)
	}
	dir := filepath.Join(t.TempDir(), "media")
	media, err := mediastore.Open(ctx, mediastore.Config{
		Dir:          dir,
		Index:        index,
		ContentTypes: []string{"audio/ogg", "audio/mp4"},
	})
	if err != nil {
		t.Fatalf("open media: %v", err)
	}
	return render.StoreMedia(media, index), dir
}

func blobFile(dir, blobID string) string {
	return filepath.Join(dir, blobID)
}

func mustExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, args)
	}
}

func addOwner(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	mustExec(t, db, "INSERT INTO users (id, kind, created_at, last_seen_at) VALUES (?, 'guest', 1, 2)", id)
}

func addEpisode(t *testing.T, db *sql.DB, id, owner string) {
	t.Helper()
	mustExec(t, db, "INSERT INTO episodes (id, owner_id, number, title, state, visibility, share_token, seeded) VALUES (?, ?, 1, 'Draft', 'draft', 'private', ?, 0)",
		id, owner, "share-"+id)
}

func addWords(t *testing.T, db *sql.DB, owner, episode string, count int, stepMs, lenMs int64) {
	t.Helper()
	for i := 0; i < count; i++ {
		start := int64(i) * stepMs
		mustExec(t, db, "INSERT INTO words (id, owner_id, episode_id, text, start_ms, end_ms, source) VALUES (?, ?, ?, ?, ?, ?, 'edit')",
			"word-"+episode+"-"+strconv.Itoa(i), owner, episode, "w"+strconv.Itoa(i), start, start+lenMs)
	}
}

func addProposal(t *testing.T, db *sql.DB, id, owner, episode, kind string, start, end int) {
	t.Helper()
	mustExec(t, db, "INSERT INTO proposals (id, owner_id, episode_id, kind, start_word, end_word, reason) VALUES (?, ?, ?, ?, ?, ?, 'test')",
		id, owner, episode, kind, start, end)
}

func addDecision(t *testing.T, db *sql.DB, id, owner, episode, proposal, decision string) {
	t.Helper()
	mustExec(t, db, "INSERT INTO decisions (id, owner_id, episode_id, proposal_id, decision) VALUES (?, ?, ?, ?, ?)",
		id, owner, episode, proposal, decision)
}

func writeTone(t *testing.T, path string, freqHz, seconds float64, rate int) {
	t.Helper()
	n := int(seconds * float64(rate))
	raw := make([]byte, 44+n*2)
	copy(raw[0:], "RIFF")
	binary.LittleEndian.PutUint32(raw[4:], uint32(36+n*2))
	copy(raw[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(raw[16:], 16)
	binary.LittleEndian.PutUint16(raw[20:], 1)
	binary.LittleEndian.PutUint16(raw[22:], 1)
	binary.LittleEndian.PutUint32(raw[24:], uint32(rate))
	binary.LittleEndian.PutUint32(raw[28:], uint32(rate*2))
	binary.LittleEndian.PutUint16(raw[32:], 2)
	binary.LittleEndian.PutUint16(raw[34:], 16)
	copy(raw[36:], "data")
	binary.LittleEndian.PutUint32(raw[40:], uint32(n*2))
	for i := 0; i < n; i++ {
		s := int16(16000 * math.Sin(2*math.Pi*freqHz*float64(i)/float64(rate)))
		binary.LittleEndian.PutUint16(raw[44+i*2:], uint16(s))
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("write tone: %v", err)
	}
}

func newResolver(db *sql.DB, media render.Media, workDir string, locate render.Stems) *render.Resolver {
	return &render.Resolver{
		DB:      db,
		Media:   media,
		WorkDir: workDir,
		Locate:  locate,
	}
}

func fixedStems(userPath, hostPath string) render.Stems {
	return func(ctx context.Context, ownerID, episodeID string) (string, string, int64, int64, error) {
		return userPath, hostPath, 0, 0, nil
	}
}

func ebur128Integrated(t *testing.T, path string) float64 {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "ffmpeg", "-hide_banner", "-i", path,
		"-af", "ebur128=peak=true", "-f", "null", "-")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("ebur128 run: %v: %s", err, stderr.String())
	}
	m := regexp.MustCompile(`(?m)^\s*I:\s+(-?\d+(?:\.\d+)?)\s+LUFS`).FindStringSubmatch(stderr.String())
	if m == nil {
		t.Fatalf("ebur128 summary missing in: %s", stderr.String())
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		t.Fatalf("ebur128 parse %q: %v", m[1], err)
	}
	return v
}

func decodeMono(t *testing.T, path string) []float64 {
	t.Helper()
	raw := filepath.Join(t.TempDir(), "raw.pcm")
	cmd := exec.CommandContext(t.Context(), "ffmpeg", "-hide_banner", "-y", "-i", path,
		"-ar", "48000", "-ac", "1", "-c:a", "pcm_s16le", "-f", "s16le", raw)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("decode: %v: %s", err, stderr.String())
	}
	buf, err := os.ReadFile(raw)
	if err != nil {
		t.Fatalf("read decoded: %v", err)
	}
	out := make([]float64, len(buf)/2)
	for i := range out {
		out[i] = float64(int16(binary.LittleEndian.Uint16(buf[i*2:]))) / 32768
	}
	return out
}

func probeDuration(t *testing.T, path string) float64 {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "ffprobe", "-hide_banner", "-v", "error",
		"-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", path)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("probe duration: %v", err)
	}
	v, err := strconv.ParseFloat(string(bytes.TrimSpace(out)), 64)
	if err != nil {
		t.Fatalf("parse duration: %v", err)
	}
	return v
}

func stemDurationMs(t *testing.T, userPath, hostPath string) int64 {
	t.Helper()
	return max(int64(math.Round(probeDuration(t, userPath)*1000)), int64(math.Round(probeDuration(t, hostPath)*1000)))
}

func toRanges(cuts []render.Cut) []render.RangeMs {
	out := make([]render.RangeMs, 0, len(cuts))
	for _, c := range cuts {
		out = append(out, render.RangeMs{Start: c.StartMs, End: c.EndMs})
	}
	return out
}

// planBounds rebuilds the join positions the run planned, reading the
// same decision rows the run read. The windows land exactly on the true
// joins, so a hard cut cannot hide between a guessed position and a
// tolerance.
func planBounds(t *testing.T, db *sql.DB, episodeID, userPath, hostPath string) []float64 {
	t.Helper()
	cuts, err := render.AcceptedCuts(t.Context(), db, episodeID)
	if err != nil {
		t.Fatalf("accepted cuts: %v", err)
	}
	cold, err := render.ColdOpen(t.Context(), db, episodeID)
	if err != nil {
		t.Fatalf("cold open: %v", err)
	}
	kept := render.KeepRanges(stemDurationMs(t, userPath, hostPath), toRanges(cuts))
	var coldKept []render.RangeMs
	if cold != nil {
		coldKept = render.IntersectRanges(*cold, kept)
	}
	return render.JoinTimes(kept, coldKept)
}

func TestRenderCutsColdOpenLoudness(t *testing.T) {
	sqliteDB, db := openDiary(t)
	_ = sqliteDB
	media, mediaDir := openMedia(t, sqliteDB)
	workDir := t.TempDir()
	userPath := filepath.Join(workDir, "user.wav")
	hostPath := filepath.Join(workDir, "host.wav")
	writeTone(t, userPath, 440.0417, 30, 44100)
	writeTone(t, hostPath, 660.0417, 30, 44100)

	addOwner(t, db, "owner-1")
	addEpisode(t, db, "ep-1", "owner-1")
	addWords(t, db, "owner-1", "ep-1", 100, 300, 250)
	addProposal(t, db, "cut-1", "owner-1", "ep-1", editorial.KindCut, 20, 39)
	addDecision(t, db, "dec-1", "owner-1", "ep-1", "cut-1", editorial.DecisionAccepted)
	addProposal(t, db, "cold-1", "owner-1", "ep-1", editorial.KindColdOpen, 50, 89)

	r := newResolver(db, media, workDir, fixedStems(userPath, hostPath))
	res, err := r.Run(t.Context(), "owner-1", "ep-1", nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Hash == "" || res.OpusMediaID == "" || res.AACMediaID == "" {
		t.Fatalf("run left ids empty: %+v", res)
	}
	if res.Reused {
		t.Fatalf("first run must not reuse")
	}

	opusBlob, err := media.Get(t.Context(), res.OpusMediaID)
	if err != nil {
		t.Fatalf("get opus blob: %v", err)
	}
	if opusBlob.ContentType != "audio/ogg" || opusBlob.Visibility != mediastore.Private {
		t.Fatalf("opus blob not private ogg: %+v", opusBlob)
	}
	aacBlob, err := media.Get(t.Context(), res.AACMediaID)
	if err != nil {
		t.Fatalf("get aac blob: %v", err)
	}
	if aacBlob.ContentType != "audio/mp4" || aacBlob.Visibility != mediastore.Private {
		t.Fatalf("aac blob not private mp4: %+v", aacBlob)
	}

	if loud := ebur128Integrated(t, blobFile(mediaDir, res.OpusMediaID)); math.Abs(loud+16) > 1 {
		t.Fatalf("ebur128 reads %v LUFS, want -16 within 1", loud)
	}

	samples := decodeMono(t, blobFile(mediaDir, res.OpusMediaID))
	assertNoClicks(t, samples, 48000, planBounds(t, db, "ep-1", userPath, hostPath))

	if out := os.Getenv("RENDER_DEBUG_OUT"); out != "" {
		raw, err := os.ReadFile(blobFile(mediaDir, res.OpusMediaID))
		if err != nil {
			t.Fatalf("read opus: %v", err)
		}
		if err := os.WriteFile(out, raw, 0o644); err != nil {
			t.Fatalf("save opus: %v", err)
		}
	}
	opusDur := probeDuration(t, blobFile(mediaDir, res.OpusMediaID))
	if math.Abs(opusDur-36.72) > 0.2 {
		t.Fatalf("opus duration %v, want 36.72 within 0.2", opusDur)
	}

	again, err := r.Run(t.Context(), "owner-1", "ep-1", nil)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if !again.Reused || again.Hash != res.Hash {
		t.Fatalf("second run must reuse the hash: %+v", again)
	}
	if again.OpusMediaID != res.OpusMediaID || again.AACMediaID != res.AACMediaID {
		t.Fatalf("second run changed the blobs: %+v", again)
	}
}

func assertNoClicks(t *testing.T, samples []float64, rate int, bounds []float64) {
	t.Helper()
	if len(bounds) == 0 {
		t.Fatalf("no boundaries to probe")
	}
	secOf := func(i int) float64 { return float64(i) / float64(rate) }
	near := func(i int) bool {
		sec := secOf(i)
		if sec < 0.1 || sec > secOf(len(samples))-0.1 {
			return false
		}
		for _, b := range bounds {
			if math.Abs(sec-b) <= 0.05 {
				return true
			}
		}
		return false
	}
	nearMax, farMax := 0.0, 0.0
	for i := 1; i < len(samples); i++ {
		d := math.Abs(samples[i] - samples[i-1])
		if near(i) {
			nearMax = math.Max(nearMax, d)
		} else {
			farMax = math.Max(farMax, d)
		}
	}
	if farMax < 0.005 {
		t.Fatalf("probe signal too quiet to judge clicks: far max %v", farMax)
	}
	// A crossfade sums two fading sides, so its slope can pass the tone
	// slope underneath by the fade term. Twice the material max holds a
	// correct join with wide margin and still fails a hard cut step,
	// which overshoots by an order of magnitude. The window peak bar
	// below carries the strict reading: a fade never raises amplitude.
	if nearMax > 2*farMax {
		t.Fatalf("click at a boundary: near max %v beats twice far max %v", nearMax, farMax)
	}
	window := rate / 100
	peakAt := func(center float64) float64 {
		c := int(center * float64(rate))
		peak := 0.0
		for i := c - window/2; i < c+window/2; i++ {
			if i >= 0 && i < len(samples) {
				peak = math.Max(peak, math.Abs(samples[i]))
			}
		}
		return peak
	}
	boundaryPeak := 0.0
	for _, b := range bounds {
		boundaryPeak = math.Max(boundaryPeak, peakAt(b))
	}
	restPeak := 0.0
	for s := 0.2; s < secOf(len(samples))-0.2; s += 0.01 {
		nearBoundary := false
		for _, b := range bounds {
			if math.Abs(s-b) <= 0.05 {
				nearBoundary = true
			}
		}
		if !nearBoundary {
			restPeak = math.Max(restPeak, peakAt(s))
		}
	}
	if boundaryPeak > restPeak {
		t.Fatalf("boundary peak %v beats the rest peak %v", boundaryPeak, restPeak)
	}
}

func TestRenderResumeReachesSameHash(t *testing.T) {
	sqliteDB, db := openDiary(t)
	media, _ := openMedia(t, sqliteDB)
	workDir := t.TempDir()
	userPath := filepath.Join(workDir, "user.wav")
	hostPath := filepath.Join(workDir, "host.wav")
	writeTone(t, userPath, 440, 8, 44100)
	writeTone(t, hostPath, 520, 8, 44100)

	addOwner(t, db, "owner-r")
	addEpisode(t, db, "ep-r", "owner-r")
	addWords(t, db, "owner-r", "ep-r", 20, 300, 250)

	r := newResolver(db, media, workDir, fixedStems(userPath, hostPath))
	first, err := r.Run(t.Context(), "owner-r", "ep-r", nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	detail, err := json.Marshal(map[string]string{"owner_id": "owner-r", "episode_id": "ep-r"})
	if err != nil {
		t.Fatalf("marshal descriptor: %v", err)
	}
	fn, err := r.Resume(job.Record{Progress: job.Progress{Stage: "start", Detail: detail}})
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	data, err := fn(t.Context(), func(job.Progress) {})
	if err != nil {
		t.Fatalf("resumed func: %v", err)
	}
	var resumed render.Result
	if err := json.Unmarshal(data, &resumed); err != nil {
		t.Fatalf("decode resumed result: %v", err)
	}
	if resumed.Hash != first.Hash || !resumed.Reused {
		t.Fatalf("resume must reuse the hash: first %+v resumed %+v", first, resumed)
	}
	if _, err := r.Resume(job.Record{}); err == nil {
		t.Fatalf("resume without a descriptor must fail")
	}
}

func TestRenderCancelledLeavesNoRow(t *testing.T) {
	sqliteDB, db := openDiary(t)
	media, _ := openMedia(t, sqliteDB)
	workDir := t.TempDir()
	userPath := filepath.Join(workDir, "user.wav")
	hostPath := filepath.Join(workDir, "host.wav")
	writeTone(t, userPath, 440, 4, 44100)
	writeTone(t, hostPath, 520, 4, 44100)

	addOwner(t, db, "owner-c")
	addEpisode(t, db, "ep-c", "owner-c")
	addWords(t, db, "owner-c", "ep-c", 10, 300, 250)

	r := newResolver(db, media, workDir, fixedStems(userPath, hostPath))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := r.Run(ctx, "owner-c", "ep-c", nil); err == nil {
		t.Fatalf("cancelled run must fail")
	}
	var count int
	if err := db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM renders WHERE episode_id = ?", "ep-c").Scan(&count); err != nil {
		t.Fatalf("count renders: %v", err)
	}
	if count != 0 {
		t.Fatalf("cancelled run stored %d rows", count)
	}
}

func TestRenderAllCutFails(t *testing.T) {
	sqliteDB, db := openDiary(t)
	media, _ := openMedia(t, sqliteDB)
	workDir := t.TempDir()
	userPath := filepath.Join(workDir, "user.wav")
	hostPath := filepath.Join(workDir, "host.wav")
	writeTone(t, userPath, 440, 3, 44100)
	writeTone(t, hostPath, 520, 3, 44100)

	addOwner(t, db, "owner-e")
	addEpisode(t, db, "ep-e", "owner-e")
	addWords(t, db, "owner-e", "ep-e", 10, 300, 300)
	addProposal(t, db, "cut-e", "owner-e", "ep-e", editorial.KindCut, 0, 9)
	addDecision(t, db, "dec-e", "owner-e", "ep-e", "cut-e", editorial.DecisionAccepted)

	r := newResolver(db, media, workDir, fixedStems(userPath, hostPath))
	if _, err := r.Run(t.Context(), "owner-e", "ep-e", nil); err == nil {
		t.Fatalf("all-cut run must fail")
	}
}

func TestRenderRevertedCutStays(t *testing.T) {
	sqliteDB, db := openDiary(t)
	media, _ := openMedia(t, sqliteDB)
	workDir := t.TempDir()
	userPath := filepath.Join(workDir, "user.wav")
	hostPath := filepath.Join(workDir, "host.wav")
	writeTone(t, userPath, 440, 6, 44100)
	writeTone(t, hostPath, 520, 6, 44100)

	addOwner(t, db, "owner-v")
	addEpisode(t, db, "ep-v", "owner-v")
	addWords(t, db, "owner-v", "ep-v", 20, 300, 250)
	addProposal(t, db, "cut-v", "owner-v", "ep-v", editorial.KindCut, 5, 9)
	addDecision(t, db, "dec-v1", "owner-v", "ep-v", "cut-v", editorial.DecisionAccepted)
	addDecision(t, db, "dec-v2", "owner-v", "ep-v", "cut-v", "withdrawn")

	r := newResolver(db, media, workDir, fixedStems(userPath, hostPath))
	res, err := r.Run(t.Context(), "owner-v", "ep-v", nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	_ = res
	cuts, err := render.AcceptedCuts(t.Context(), db, "ep-v")
	if err != nil {
		t.Fatalf("accepted cuts: %v", err)
	}
	if len(cuts) != 0 {
		t.Fatalf("reverted cut still applies: %+v", cuts)
	}
}

func TestRenderFixtureStems(t *testing.T) {
	sqliteDB, db := openDiary(t)
	media, mediaDir := openMedia(t, sqliteDB)
	workDir := t.TempDir()
	userPath := filepath.Join("..", "..", "testdata", "sessions", "steady", "user-a.wav")
	hostPath := filepath.Join("..", "..", "testdata", "sessions", "steady", "user-b.wav")
	if _, err := os.Stat(userPath); err != nil {
		t.Skipf("fixture stems absent: %v", err)
	}

	addOwner(t, db, "owner-f")
	addEpisode(t, db, "ep-f", "owner-f")
	addWords(t, db, "owner-f", "ep-f", 10, 300, 250)
	addProposal(t, db, "cut-f", "owner-f", "ep-f", editorial.KindCut, 3, 5)
	addDecision(t, db, "dec-f", "owner-f", "ep-f", "cut-f", editorial.DecisionAccepted)

	r := newResolver(db, media, workDir, fixedStems(userPath, hostPath))
	res, err := r.Run(t.Context(), "owner-f", "ep-f", nil)
	_ = res
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if loud := ebur128Integrated(t, blobFile(mediaDir, res.OpusMediaID)); math.Abs(loud+16) > 1 {
		t.Fatalf("fixture ebur128 reads %v LUFS, want -16 within 1", loud)
	}
	opusFile := blobFile(mediaDir, res.OpusMediaID)
	if out := os.Getenv("RENDER_DEBUG_OUT"); out != "" {
		raw, err := os.ReadFile(opusFile)
		if err != nil {
			t.Fatalf("read opus: %v", err)
		}
		if err := os.WriteFile(out, raw, 0o644); err != nil {
			t.Fatalf("save opus: %v", err)
		}
	}
	assertNoClicks(t, decodeMono(t, opusFile), 48000, planBounds(t, db, "ep-f", userPath, hostPath))
}
