package render_test

import (
	"context"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/nrynss/reprise/internal/render"
)

func writeSilence(t *testing.T, path string, seconds float64, rate int) {
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
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("write silence: %v", err)
	}
}

func writePlacedTone(t *testing.T, path string, freqHz, totalSecs, toneStartSecs, toneSecs float64, rate int) {
	t.Helper()
	n := int(totalSecs * float64(rate))
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
	first := int(toneStartSecs * float64(rate))
	last := first + int(toneSecs*float64(rate))
	for i := first; i < last && i < n; i++ {
		s := int16(16000 * math.Sin(2*math.Pi*freqHz*float64(i)/float64(rate)))
		binary.LittleEndian.PutUint16(raw[44+i*2:], uint16(s))
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("write placed tone: %v", err)
	}
}

func windowRMS(samples []float64, rate int, centerSecs, halfWidthSecs float64) float64 {
	center := int(centerSecs * float64(rate))
	half := int(halfWidthSecs * float64(rate))
	var sum float64
	var count int
	for i := center - half; i < center+half; i++ {
		if i >= 0 && i < len(samples) {
			sum += samples[i] * samples[i]
			count++
		}
	}
	if count == 0 {
		return 0
	}
	return math.Sqrt(sum / float64(count))
}

// TestPreviewMixesBothStemsOnTheEpisodeClock builds a preview from a
// silent user stem and a host stem carrying a 1 kHz tone at 2.0 s, with
// the host stem offset by 500 ms. A sample scan of the output must find
// the tone at 2.5 s within 20 ms. Dropping the host input fails the run,
// so the scan proves the host stem reached the mix.
func TestPreviewMixesBothStemsOnTheEpisodeClock(t *testing.T) {
	sqliteDB, db := openDiary(t)
	media, mediaDir := openMedia(t, sqliteDB)
	workDir := t.TempDir()
	userPath := filepath.Join(workDir, "user.wav")
	hostPath := filepath.Join(workDir, "host.wav")
	writeSilence(t, userPath, 6, 44100)
	writePlacedTone(t, hostPath, 1000, 6, 2.0, 0.4, 44100)

	addOwner(t, db, "owner-pv")
	addEpisode(t, db, "ep-pv", "owner-pv")

	r := newResolver(db, media, workDir, func(ctx context.Context, ownerID, episodeID string) (string, string, int64, int64, error) {
		_ = ctx
		_ = ownerID
		_ = episodeID
		return userPath, hostPath, 0, 500, nil
	})
	res, err := r.Preview(t.Context(), "owner-pv", "ep-pv", nil)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if res.Hash == "" || res.MediaID == "" {
		t.Fatalf("preview left ids empty: %+v", res)
	}
	if res.Reused {
		t.Fatalf("first preview must not reuse")
	}

	samples := decodeMono(t, blobFile(mediaDir, res.MediaID))
	const rate = 48000
	tone := windowRMS(samples, rate, 2.5, 0.02)
	quiet := windowRMS(samples, rate, 0.8, 0.5)
	if tone < 0.03 {
		t.Fatalf("tone RMS at 2.5 s = %v, want at least 0.03", tone)
	}
	if tone < 10*quiet {
		t.Fatalf("tone RMS %v is less than ten times the quiet RMS %v", tone, quiet)
	}

	again, err := r.Preview(t.Context(), "owner-pv", "ep-pv", nil)
	if err != nil {
		t.Fatalf("second preview: %v", err)
	}
	if !again.Reused || again.Hash != res.Hash || again.MediaID != res.MediaID {
		t.Fatalf("second preview must reuse the row: first %+v again %+v", res, again)
	}
}

// TestPreviewNeedsBothStems drops the host input and requires the run
// to fail. A preview that mixed silence alone would still decode, so
// only the error proves the host stem is load-bearing.
func TestPreviewNeedsBothStems(t *testing.T) {
	sqliteDB, db := openDiary(t)
	media, _ := openMedia(t, sqliteDB)
	workDir := t.TempDir()
	userPath := filepath.Join(workDir, "user.wav")
	writeSilence(t, userPath, 2, 44100)

	addOwner(t, db, "owner-ph")
	addEpisode(t, db, "ep-ph", "owner-ph")

	r := newResolver(db, media, workDir, func(ctx context.Context, ownerID, episodeID string) (string, string, int64, int64, error) {
		_ = ctx
		_ = ownerID
		_ = episodeID
		return userPath, "", 0, 500, nil
	})
	if _, err := r.Preview(t.Context(), "owner-ph", "ep-ph", nil); err == nil {
		t.Fatal("preview without the host stem succeeded, want the missing stem refusal")
	}
}

// TestPreviewRowRoundTrip stores one preview row and requires the newest
// read to return its blob. An episode with no preview row reads empty.
func TestPreviewRowRoundTrip(t *testing.T) {
	_, db := openDiary(t)
	addOwner(t, db, "owner-pr")
	addEpisode(t, db, "ep-pr", "owner-pr")
	addOwner(t, db, "owner-bare")
	addEpisode(t, db, "ep-bare", "owner-bare")

	if got, err := render.PreviewMediaID(t.Context(), db, "owner-bare", "ep-bare"); err != nil || got != "" {
		t.Fatalf("bare preview media = %q, %v, want empty with no error", got, err)
	}
	res := render.PreviewResult{Hash: "hash-preview-1", MediaID: "preview-blob-1"}
	if err := render.StorePreview(t.Context(), db, "owner-pr", "ep-pr", res); err != nil {
		t.Fatalf("store preview: %v", err)
	}
	got, err := render.PreviewMediaID(t.Context(), db, "owner-pr", "ep-pr")
	if err != nil {
		t.Fatalf("preview media: %v", err)
	}
	if got != "preview-blob-1" {
		t.Fatalf("preview media = %q, want the stored blob", got)
	}
	if err := render.StorePreview(t.Context(), db, "owner-pr", "ep-pr", render.PreviewResult{}); err == nil {
		t.Fatal("empty preview stored without a refusal")
	}
}
