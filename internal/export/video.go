package export

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os/exec"
	"strconv"
	"strings"

	"github.com/nrynss/keel/ffmpeg"
	"github.com/nrynss/keel/waveform"
)

// Tools names the ffmpeg and ffprobe executables a video build invokes.
// It aliases the library type so wiring passes its tools straight
// through.
type Tools = ffmpeg.Tools

// BuildVideo draws the audio waveform over the still cover and writes
// one 1920 by 1080 video to out. The still keeps its shape with padding
// where its aspect differs, and the audio stream passes through without
// re-encoding, so out names an MP4 whenever the input audio is AAC. The
// context kills the child when it ends.
func BuildVideo(ctx context.Context, tools Tools, stillPath, audioPath, outPath string) error {
	if stillPath == "" || audioPath == "" || outPath == "" {
		return fmt.Errorf("export: build video: %w: empty path", ErrInvalid)
	}
	if err := waveform.Render(ctx, tools, waveform.Config{Width: Width, Height: Height}, stillPath, audioPath, outPath); err != nil {
		return fmt.Errorf("export: build video: %w", err)
	}
	return nil
}

// VideoInfo reports what ffprobe measures on one built video.
type VideoInfo struct {
	// Width and Height measure the video frame in pixels.
	Width int
	// Height measures the video frame in pixels.
	Height int
	// DurationMs is the container length in milliseconds.
	DurationMs int64
	// HasAudio reports a decoded audio stream exists.
	HasAudio bool
	// AudioCodec names the audio stream codec, such as aac.
	AudioCodec string
}

// probeStream is the subset of one ffprobe stream object this package
// reads.
type probeStream struct {
	CodecType string `json:"codec_type"`
	CodecName string `json:"codec_name"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
}

// probeFormat is the container-level subset this package reads.
type probeFormat struct {
	Duration string `json:"duration"`
}

// probeJSON is the response of an ffprobe run that asks for streams and
// format.
type probeJSON struct {
	Streams []probeStream `json:"streams"`
	Format  probeFormat   `json:"format"`
}

// ProbeVideo measures one built video with ffprobe. Callers assert the
// frame reads 1920 by 1080, the audio stream exists, and the duration
// matches the render the video was built from.
func ProbeVideo(ctx context.Context, tools Tools, path string) (VideoInfo, error) {
	if path == "" {
		return VideoInfo{}, fmt.Errorf("export: probe video: %w: empty path", ErrInvalid)
	}
	bin := tools.FFprobe
	if bin == "" {
		bin = "ffprobe"
	}
	cmd := exec.CommandContext(ctx, bin,
		"-v", "error",
		"-show_streams",
		"-show_format",
		"-of", "json",
		path)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return VideoInfo{}, fmt.Errorf("export: probe video %s: %s: %w", path, strings.TrimSpace(stderr.String()), err)
	}
	var parsed probeJSON
	if err := json.Unmarshal(stdout.Bytes(), &parsed); err != nil {
		return VideoInfo{}, fmt.Errorf("export: probe video %s: %w", path, err)
	}
	var info VideoInfo
	for _, s := range parsed.Streams {
		switch s.CodecType {
		case "video":
			if info.Width == 0 && info.Height == 0 {
				info.Width = s.Width
				info.Height = s.Height
			}
		case "audio":
			if !info.HasAudio {
				info.HasAudio = true
				info.AudioCodec = s.CodecName
			}
		}
	}
	if info.Width == 0 || info.Height == 0 {
		return VideoInfo{}, fmt.Errorf("export: probe video %s: %w: no video stream", path, ErrInvalid)
	}
	secs, err := strconv.ParseFloat(strings.TrimSpace(parsed.Format.Duration), 64)
	if err != nil {
		return VideoInfo{}, fmt.Errorf("export: probe video %s: %w: bad duration %q", path, ErrInvalid, parsed.Format.Duration)
	}
	info.DurationMs = int64(math.Round(secs * 1000))
	return info, nil
}
