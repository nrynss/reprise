package export_test

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/nrynss/reprise/internal/export"
)

// fixtureDurationMs is the render length every fixture describes. Three
// ten second chapters fit inside it with room at both ends.
const fixtureDurationMs = int64(35000)

// makeAAC renders a fixed length AAC file and returns its path. The
// tone is generated, so no person and no microphone is involved.
func makeAAC(t *testing.T, secs int) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "render.m4a")
	cmd := exec.CommandContext(t.Context(), "ffmpeg", "-hide_banner", "-y",
		"-f", "lavfi", "-i", "sine=frequency=440:duration="+strconv.Itoa(secs),
		"-c:a", "aac", "-b:a", "128k", out)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("render aac: %v: %s", err, stderr.String())
	}
	return out
}

// makeCoverPNG draws a deterministic square panel and returns its bytes.
// A diagonal gradient keeps real colour variation in every row.
func makeCoverPNG(t *testing.T, size int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			img.Set(x, y, color.RGBA{uint8((x * 255) / size), uint8((y * 255) / size), 128, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode cover: %v", err)
	}
	return buf.Bytes()
}

// writeStill stores cover bytes as a PNG file and returns its path.
func writeStill(t *testing.T, cover []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cover.png")
	if err := os.WriteFile(path, cover, 0o600); err != nil {
		t.Fatalf("write still: %v", err)
	}
	return path
}

// makeWords spreads one word per step across durationMs, so captions
// cover the whole render the chapters describe.
func makeWords(t *testing.T, durationMs int64, stepMs int64) []export.Word {
	t.Helper()
	pool := []string{"the", "host", "asks", "again", "next", "week", "about", "the", "thing", "you", "said"}
	var out []export.Word
	i := 0
	for start := int64(0); start+stepMs <= durationMs; start += stepMs {
		out = append(out, export.Word{Text: pool[i%len(pool)], StartMs: start, EndMs: start + stepMs})
		i++
	}
	if len(out) == 0 {
		t.Fatalf("make words: no words fit in %d ms", durationMs)
	}
	return out
}

// fixtureChapters returns three chapters the upload form accepts over
// the fixture duration. Every start matches a word start from makeWords
// with a 500 ms step, so seeking lands on speech.
func fixtureChapters() []export.Chapter {
	return []export.Chapter{
		{Title: "Opening", StartMs: 0},
		{Title: "The middle", StartMs: 12000},
		{Title: "The close", StartMs: 24000},
	}
}

// probeDurationMs reads the container length of one media file with
// ffprobe, independent of the code under test.
func probeDurationMs(t *testing.T, path string) int64 {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "ffprobe", "-hide_banner", "-v", "error",
		"-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", path)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("probe duration: %v", err)
	}
	secs, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil {
		t.Fatalf("parse duration: %v", err)
	}
	return int64(secs*1000 + 0.5)
}
