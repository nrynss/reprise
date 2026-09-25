package export_test

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nrynss/reprise/internal/export"
)

// unzipBundle reads every bundle entry into memory in zip order. It
// fails the test on any read fault, so the pins measure the bundle and
// not the test.
func unzipBundle(t *testing.T, raw []byte) ([]string, map[string][]byte) {
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
		raw, err := io.ReadAll(body)
		closeErr := body.Close()
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name, err)
		}
		if closeErr != nil {
			t.Fatalf("close %s: %v", closeErr, err)
		}
		order = append(order, entry.Name)
		files[entry.Name] = raw
	}
	return order, files
}

// probeAudioCodec reads the first audio codec with ffprobe,
// independent of the code under test.
func probeAudioCodec(t *testing.T, path string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "ffprobe", "-hide_banner", "-v", "error",
		"-select_streams", "a:0", "-show_entries", "stream=codec_name",
		"-of", "default=noprint_wrappers=1:nokey=1", path)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("probe audio codec: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// probeFrame reads the video frame size with ffprobe, independent of
// the code under test.
func probeFrame(t *testing.T, path string) (int, int) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "ffprobe", "-hide_banner", "-v", "error",
		"-select_streams", "v:0", "-show_entries", "stream=width,height",
		"-of", "csv=p=0", path)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("probe frame: %v", err)
	}
	var width, height int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(out)), "%d,%d", &width, &height); err != nil {
		t.Fatalf("parse frame: %v", err)
	}
	return width, height
}

func TestBundleHoldsEveryFile(t *testing.T) {
	audioPath := makeAAC(t, 35)
	audio, err := os.ReadFile(audioPath)
	if err != nil {
		t.Fatalf("read aac: %v", err)
	}
	duration := probeDurationMs(t, audioPath)
	notes := "The host asks again next week about the thing you said."
	in := export.Inputs{
		Title:      "A quiet take",
		Number:     4,
		Audio:      audio,
		Cover:      makeCoverPNG(t, 512),
		Words:      makeWords(t, fixtureDurationMs, 500),
		Chapters:   fixtureChapters(),
		ShowNotes:  notes,
		DurationMs: duration,
	}
	var bundle bytes.Buffer
	if err := export.WriteBundle(t.Context(), export.Tools{}, in, &bundle); err != nil {
		t.Fatalf("write bundle: %v", err)
	}
	order, files := unzipBundle(t, bundle.Bytes())
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
	audioOut := filepath.Join(dir, "audio.m4a")
	if err := os.WriteFile(audioOut, files[export.FileAudio], 0o600); err != nil {
		t.Fatalf("write audio: %v", err)
	}
	if codec := probeAudioCodec(t, audioOut); codec != "aac" {
		t.Fatalf("bundle audio codec is %q, want aac", codec)
	}
	videoOut := filepath.Join(dir, "video.mp4")
	if err := os.WriteFile(videoOut, files[export.FileVideo], 0o600); err != nil {
		t.Fatalf("write video: %v", err)
	}
	if width, height := probeFrame(t, videoOut); width != 1920 || height != 1080 {
		t.Fatalf("bundle video frame is %dx%d, want 1920x1080", width, height)
	}
	if codec := probeAudioCodec(t, videoOut); codec != "aac" {
		t.Fatalf("bundle video audio codec is %q, want aac", codec)
	}
	if cues := parseCues(t, string(files[export.FileSRT]), srtTiming); cues[0][0] != 0 {
		t.Fatalf("first srt cue starts at %d, want 0", cues[0][0])
	}
	rest := strings.TrimSpace(strings.TrimPrefix(string(files[export.FileVTT]), "WEBVTT"))
	if cues := parseCues(t, rest, vttTiming); cues[0][0] != 0 {
		t.Fatalf("first vtt cue starts at %d, want 0", cues[0][0])
	}
	cover, err := png.DecodeConfig(bytes.NewReader(files[export.FileCover]))
	if err != nil {
		t.Fatalf("decode bundle cover: %v", err)
	}
	if cover.Width != cover.Height {
		t.Fatalf("bundle cover is %dx%d, want a square", cover.Width, cover.Height)
	}
	description := string(files[export.FileDescription])
	if !strings.HasPrefix(description, notes) {
		t.Fatalf("description does not open with the show notes: %q", description)
	}
	for _, want := range []string{"0:00 Opening", "0:12 The middle", "0:24 The close"} {
		if !strings.Contains(description, want) {
			t.Fatalf("description misses chapter line %q: %q", want, description)
		}
	}
}

func TestBundleRejects(t *testing.T) {
	good := func() export.Inputs {
		return export.Inputs{
			Title:      "A quiet take",
			Number:     1,
			Audio:      []byte("fake-aac"),
			Cover:      makeCoverPNG(t, 128),
			Words:      makeWords(t, fixtureDurationMs, 500),
			Chapters:   fixtureChapters(),
			ShowNotes:  "The host asks again next week.",
			DurationMs: fixtureDurationMs,
		}
	}
	cases := map[string]struct {
		change func(*export.Inputs)
		want   error
	}{
		"empty audio":   {func(in *export.Inputs) { in.Audio = nil }, export.ErrInvalid},
		"empty cover":   {func(in *export.Inputs) { in.Cover = nil }, export.ErrInvalid},
		"no words":      {func(in *export.Inputs) { in.Words = nil }, export.ErrNoWords},
		"short chapter": {func(in *export.Inputs) { in.Chapters = in.Chapters[:2] }, export.ErrChapters},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			in := good()
			tc.change(&in)
			if err := export.WriteBundle(t.Context(), export.Tools{}, in, &bytes.Buffer{}); !errors.Is(err, tc.want) {
				t.Fatalf("bundle error = %v, want %v", err, tc.want)
			}
		})
	}
}
