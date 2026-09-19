package export_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/nrynss/reprise/internal/export"
)

// srtTiming matches one SubRip timing line with a comma decimal.
var srtTiming = regexp.MustCompile(`^(\d{2}):(\d{2}):(\d{2}),(\d{3}) --> (\d{2}):(\d{2}):(\d{2}),(\d{3})$`)

// vttTiming matches one WebVTT timing line with a full stop decimal.
var vttTiming = regexp.MustCompile(`^(\d{2}):(\d{2}):(\d{2})\.(\d{3}) --> (\d{2}):(\d{2}):(\d{2})\.(\d{3})$`)

// stampMs parses one side of a timing line into milliseconds.
func stampMs(t *testing.T, h, m, s, ms string) int64 {
	t.Helper()
	parts := []int64{0, 0, 0, 0}
	for i, raw := range []string{h, m, s, ms} {
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			t.Fatalf("parse stamp part %q: %v", raw, err)
		}
		parts[i] = v
	}
	return ((parts[0]*60+parts[1])*60+parts[2])*1000 + parts[3]
}

// parseCues strictly validates caption blocks and returns cue timings.
// It rejects numbering gaps, bad timings, empty text, and overlapping
// cues, so a grouper defect fails the pin instead of passing quietly.
func parseCues(t *testing.T, raw string, timing *regexp.Regexp) [][2]int64 {
	t.Helper()
	blocks := strings.Split(strings.TrimSpace(raw), "\n\n")
	var cues [][2]int64
	for i, block := range blocks {
		lines := strings.Split(block, "\n")
		if len(lines) < 3 {
			t.Fatalf("block %d: want number, timing, text, got %d lines", i+1, len(lines))
		}
		if lines[0] != strconv.Itoa(i+1) {
			t.Fatalf("block %d: want number %d, got %q", i+1, i+1, lines[0])
		}
		match := timing.FindStringSubmatch(lines[1])
		if match == nil {
			t.Fatalf("block %d: bad timing line %q", i+1, lines[1])
		}
		start := stampMs(t, match[1], match[2], match[3], match[4])
		end := stampMs(t, match[5], match[6], match[7], match[8])
		if end <= start {
			t.Fatalf("block %d: end %d not past start %d", i+1, end, start)
		}
		if len(cues) > 0 && start < cues[len(cues)-1][1] {
			t.Fatalf("block %d: start %d overlaps previous end %d", i+1, start, cues[len(cues)-1][1])
		}
		text := strings.Join(lines[2:], "\n")
		if strings.TrimSpace(text) == "" {
			t.Fatalf("block %d: empty cue text", i+1)
		}
		cues = append(cues, [2]int64{start, end})
	}
	if len(cues) == 0 {
		t.Fatalf("no cues parsed")
	}
	return cues
}

func TestSRTParses(t *testing.T) {
	words := makeWords(t, fixtureDurationMs, 500)
	raw, err := export.SRT(words)
	if err != nil {
		t.Fatalf("srt: %v", err)
	}
	cues := parseCues(t, raw, srtTiming)
	if cues[0][0] != 0 {
		t.Fatalf("first cue starts at %d, want 0", cues[0][0])
	}
	if got := cues[len(cues)-1][1]; got != words[len(words)-1].EndMs {
		t.Fatalf("last cue ends at %d, want last word end %d", got, words[len(words)-1].EndMs)
	}
}

func TestWebVTTParses(t *testing.T) {
	words := makeWords(t, fixtureDurationMs, 500)
	raw, err := export.WebVTT(words)
	if err != nil {
		t.Fatalf("webvtt: %v", err)
	}
	if !strings.HasPrefix(raw, "WEBVTT") {
		t.Fatalf("webvtt missing header, got %q", raw[:min(20, len(raw))])
	}
	rest := strings.TrimSpace(strings.TrimPrefix(raw, "WEBVTT"))
	cues := parseCues(t, rest, vttTiming)
	if cues[0][0] != 0 {
		t.Fatalf("first cue starts at %d, want 0", cues[0][0])
	}
	if got := cues[len(cues)-1][1]; got != words[len(words)-1].EndMs {
		t.Fatalf("last cue ends at %d, want last word end %d", got, words[len(words)-1].EndMs)
	}
}

func TestSRTAndWebVTTShareTimings(t *testing.T) {
	words := makeWords(t, fixtureDurationMs, 500)
	srtRaw, err := export.SRT(words)
	if err != nil {
		t.Fatalf("srt: %v", err)
	}
	vttRaw, err := export.WebVTT(words)
	if err != nil {
		t.Fatalf("webvtt: %v", err)
	}
	srtCues := parseCues(t, srtRaw, srtTiming)
	vttCues := parseCues(t, strings.TrimSpace(strings.TrimPrefix(vttRaw, "WEBVTT")), vttTiming)
	if len(srtCues) != len(vttCues) {
		t.Fatalf("cue counts differ: srt %d, vtt %d", len(srtCues), len(vttCues))
	}
	for i := range srtCues {
		if srtCues[i] != vttCues[i] {
			t.Fatalf("cue %d differs: srt %v, vtt %v", i+1, srtCues[i], vttCues[i])
		}
	}
}

func TestCaptionWordsRejects(t *testing.T) {
	if _, err := export.SRT(nil); !errors.Is(err, export.ErrNoWords) {
		t.Fatalf("empty words: want ErrNoWords, got %v", err)
	}
	inverted := []export.Word{{Text: "hello", StartMs: 500, EndMs: 100}}
	if _, err := export.SRT(inverted); !errors.Is(err, export.ErrOrder) {
		t.Fatalf("inverted span: want ErrOrder, got %v", err)
	}
	shuffled := []export.Word{
		{Text: "second", StartMs: 500, EndMs: 900},
		{Text: "first", StartMs: 0, EndMs: 400},
	}
	if _, err := export.WebVTT(shuffled); !errors.Is(err, export.ErrOrder) {
		t.Fatalf("shuffled words: want ErrOrder, got %v", err)
	}
	blank := []export.Word{{Text: "  ", StartMs: 0, EndMs: 400}}
	if _, err := export.SRT(blank); !errors.Is(err, export.ErrInvalid) {
		t.Fatalf("blank text: want ErrInvalid, got %v", err)
	}
}

func TestStamp(t *testing.T) {
	cases := map[int64]string{
		0:       "0:00",
		5000:    "0:05",
		60000:   "1:00",
		90000:   "1:30",
		754000:  "12:34",
		3661000: "1:01:01",
	}
	for ms, want := range cases {
		if got := export.Stamp(ms); got != want {
			t.Fatalf("stamp %d: got %q, want %q", ms, got, want)
		}
	}
}

func TestDescriptionFollowsChapterRules(t *testing.T) {
	notes := "The host asks again next week about the thing you said."
	raw, err := export.Description(notes, fixtureChapters(), fixtureDurationMs)
	if err != nil {
		t.Fatalf("description: %v", err)
	}
	if !strings.HasPrefix(raw, notes) {
		t.Fatalf("description does not open with the show notes: %q", raw)
	}
	linePattern := regexp.MustCompile(`^(\d+):(\d{2})(?::(\d{2}))? (.+)$`)
	var starts []int64
	for _, line := range strings.Split(raw, "\n") {
		match := linePattern.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		var ms int64
		if match[3] == "" {
			mins, _ := strconv.ParseInt(match[1], 10, 64)
			secs, _ := strconv.ParseInt(match[2], 10, 64)
			ms = (mins*60 + secs) * 1000
		} else {
			hours, _ := strconv.ParseInt(match[1], 10, 64)
			mins, _ := strconv.ParseInt(match[2], 10, 64)
			secs, _ := strconv.ParseInt(match[3], 10, 64)
			ms = ((hours*60+mins)*60 + secs) * 1000
		}
		if strings.TrimSpace(match[4]) == "" {
			t.Fatalf("chapter line %q carries no title", line)
		}
		starts = append(starts, ms)
	}
	if len(starts) < 3 {
		t.Fatalf("want at least 3 chapter lines, got %d", len(starts))
	}
	if starts[0] != 0 {
		t.Fatalf("first chapter starts at %d, want 0", starts[0])
	}
	bounds := append(append([]int64(nil), starts...), fixtureDurationMs)
	for i := range starts {
		if span := bounds[i+1] - bounds[i]; span < 10000 {
			t.Fatalf("chapter %d spans %d ms, want at least 10000", i+1, span)
		}
	}
}

func TestValidateChaptersRejects(t *testing.T) {
	good := fixtureChapters()
	if err := export.ValidateChapters(good, fixtureDurationMs); err != nil {
		t.Fatalf("valid chapters: %v", err)
	}
	moved := []export.Chapter{{Title: "Late", StartMs: 5000}, {Title: "Mid", StartMs: 12000}, {Title: "End", StartMs: 24000}}
	if err := export.ValidateChapters(moved, fixtureDurationMs); !errors.Is(err, export.ErrChapters) {
		t.Fatalf("first past zero: want ErrChapters, got %v", err)
	}
	lateStart := []export.Chapter{{Title: "Late", StartMs: 5000}, {Title: "Mid", StartMs: 15000}, {Title: "End", StartMs: 25000}}
	if err := export.ValidateChapters(lateStart, fixtureDurationMs); !errors.Is(err, export.ErrChapters) {
		t.Fatalf("first past zero with full spans: want ErrChapters, got %v", err)
	}
	short := good[:2]
	if err := export.ValidateChapters(short, fixtureDurationMs); !errors.Is(err, export.ErrChapters) {
		t.Fatalf("two chapters: want ErrChapters, got %v", err)
	}
	tight := []export.Chapter{{Title: "A", StartMs: 0}, {Title: "B", StartMs: 5000}, {Title: "C", StartMs: 24000}}
	if err := export.ValidateChapters(tight, fixtureDurationMs); !errors.Is(err, export.ErrChapters) {
		t.Fatalf("short span: want ErrChapters, got %v", err)
	}
	blank := []export.Chapter{{Title: "A", StartMs: 0}, {Title: " ", StartMs: 12000}, {Title: "C", StartMs: 24000}}
	if err := export.ValidateChapters(blank, fixtureDurationMs); !errors.Is(err, export.ErrChapters) {
		t.Fatalf("blank title: want ErrChapters, got %v", err)
	}
	pastEnd := []export.Chapter{{Title: "A", StartMs: 0}, {Title: "B", StartMs: 12000}, {Title: "C", StartMs: 99000}}
	if err := export.ValidateChapters(pastEnd, fixtureDurationMs); !errors.Is(err, export.ErrChapters) {
		t.Fatalf("start past duration: want ErrChapters, got %v", err)
	}
	if err := export.ValidateChapters(good, 0); !errors.Is(err, export.ErrInvalid) {
		t.Fatalf("missing duration: want ErrInvalid, got %v", err)
	}
}

func TestVideoIs1080PWithAudio(t *testing.T) {
	ctx := context.Background()
	audioPath := makeAAC(t, 35)
	renderMs := probeDurationMs(t, audioPath)
	stillPath := writeStill(t, makeCoverPNG(t, 1024))
	outPath := filepath.Join(t.TempDir(), "episode.mp4")
	if err := export.BuildVideo(ctx, export.Tools{}, stillPath, audioPath, outPath); err != nil {
		t.Fatalf("build video: %v", err)
	}
	info, err := export.ProbeVideo(ctx, export.Tools{}, outPath)
	if err != nil {
		t.Fatalf("probe video: %v", err)
	}
	if info.Width != 1920 || info.Height != 1080 {
		t.Fatalf("frame is %dx%d, want 1920x1080", info.Width, info.Height)
	}
	if !info.HasAudio {
		t.Fatalf("video carries no audio stream")
	}
	if info.AudioCodec != "aac" {
		t.Fatalf("audio codec is %q, want aac", info.AudioCodec)
	}
	if gap := info.DurationMs - renderMs; gap < -150 || gap > 150 {
		t.Fatalf("video lasts %d ms, render lasts %d ms, gap %d past 150", info.DurationMs, renderMs, gap)
	}
}

func TestBuildVideoRejectsEmptyPaths(t *testing.T) {
	if err := export.BuildVideo(context.Background(), export.Tools{}, "", "a.m4a", "o.mp4"); !errors.Is(err, export.ErrInvalid) {
		t.Fatalf("empty still: want ErrInvalid, got %v", err)
	}
}

func TestCoverSquare(t *testing.T) {
	square := makeCoverPNG(t, 512)
	if err := export.CheckCoverSquare(square); err != nil {
		t.Fatalf("square cover: %v", err)
	}
	width, height, err := export.CoverSize(square)
	if err != nil {
		t.Fatalf("cover size: %v", err)
	}
	if width != 512 || height != 512 {
		t.Fatalf("cover size is %dx%d, want 512x512", width, height)
	}
	rect := image.NewRGBA(image.Rect(0, 0, 512, 256))
	for y := 0; y < 256; y++ {
		for x := 0; x < 512; x++ {
			rect.Set(x, y, color.RGBA{uint8(x % 256), uint8(y % 256), 0, 255})
		}
	}
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, rect); err != nil {
		t.Fatalf("encode rectangle: %v", err)
	}
	if err := export.CheckCoverSquare(pngBuf.Bytes()); !errors.Is(err, export.ErrInvalid) {
		t.Fatalf("rectangle cover: want ErrInvalid, got %v", err)
	}
	if err := export.CheckCoverSquare(nil); !errors.Is(err, export.ErrInvalid) {
		t.Fatalf("empty cover: want ErrInvalid, got %v", err)
	}
}

func TestDescriptionWithoutNotesStillChapters(t *testing.T) {
	raw, err := export.Description("", fixtureChapters(), fixtureDurationMs)
	if err != nil {
		t.Fatalf("description: %v", err)
	}
	if !strings.HasPrefix(raw, "0:00 Opening") {
		t.Fatalf("notes-free description does not open on chapters: %q", raw)
	}
}
