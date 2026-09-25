// Bundle assembly for one finished episode.
//
// The bundle holds exactly what the upload form asks for. The AAC render
// travels as the audio master. A still cover plus that audio becomes a
// 1920 by 1080 waveform video. The rendered words become captions through
// the library captioner. The show notes plus timestamped chapter lines
// become the description. The square cover art travels as is.
//
// The build works inside a temp directory the caller never sees. Audio
// and video each pass an independent probe in tests, so a reader trusts
// the files and not this comment.

package export

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Bundle file names. The episode page download and the upload form both
// read these names, so they stay stable.
const (
	// FileAudio carries the AAC render inside MP4.
	FileAudio = "audio.m4a"
	// FileVideo carries the waveform video.
	FileVideo = "video.mp4"
	// FileSRT carries the SubRip captions.
	FileSRT = "captions.srt"
	// FileVTT carries the WebVTT captions.
	FileVTT = "captions.vtt"
	// FileCover carries the square cover art.
	FileCover = "cover.png"
	// FileDescription carries the show notes with chapter lines.
	FileDescription = "description.txt"
)

// BundleOrder lists every bundle file in the order the zip carries
// them. Tests read the order back, so a dropped file fails instead of
// hiding.
var BundleOrder = []string{FileAudio, FileVideo, FileSRT, FileVTT, FileCover, FileDescription}

// Inputs carries everything one bundle build reads. Audio holds the AAC
// render bytes. Cover holds the square PNG bytes. Words hold the
// rendered words on the render clock. Chapters hold the shaped episode
// chapters. ShowNotes holds the episode notes the description opens
// with. DurationMs holds the render length the chapters describe.
type Inputs struct {
	// Title names the episode the bundle describes.
	Title string
	// Number orders the episode within its season.
	Number int64
	// Audio holds the AAC render bytes.
	Audio []byte
	// Cover holds the square PNG bytes.
	Cover []byte
	// Words holds the rendered words on the render clock.
	Words []Word
	// Chapters holds the shaped episode chapters.
	Chapters []Chapter
	// ShowNotes holds the episode notes the description opens with.
	ShowNotes string
	// DurationMs holds the render length in milliseconds.
	DurationMs int64
}

// WriteBundle builds one export bundle onto w. It validates the text
// outputs first, so a bad chapter fails before ffmpeg starts. The video
// build runs inside a temp directory that is removed before return. A
// failure wraps a sentinel the caller branches on.
func WriteBundle(ctx context.Context, tools Tools, in Inputs, w io.Writer) error {
	if len(in.Audio) == 0 {
		return fmt.Errorf("export: bundle: %w: empty audio", ErrInvalid)
	}
	if err := CheckCoverSquare(in.Cover); err != nil {
		return fmt.Errorf("export: bundle: %w", err)
	}
	srt, err := SRT(in.Words)
	if err != nil {
		return fmt.Errorf("export: bundle: %w", err)
	}
	vtt, err := WebVTT(in.Words)
	if err != nil {
		return fmt.Errorf("export: bundle: %w", err)
	}
	description, err := Description(in.ShowNotes, in.Chapters, in.DurationMs)
	if err != nil {
		return fmt.Errorf("export: bundle: %w", err)
	}
	work, err := os.MkdirTemp("", "export-bundle-*")
	if err != nil {
		return fmt.Errorf("export: bundle: %w", err)
	}
	defer os.RemoveAll(work)
	audioPath := filepath.Join(work, FileAudio)
	if err := os.WriteFile(audioPath, in.Audio, 0o600); err != nil {
		return fmt.Errorf("export: bundle: %w", err)
	}
	stillPath := filepath.Join(work, FileCover)
	if err := os.WriteFile(stillPath, in.Cover, 0o600); err != nil {
		return fmt.Errorf("export: bundle: %w", err)
	}
	videoPath := filepath.Join(work, FileVideo)
	if err := BuildVideo(ctx, tools, stillPath, audioPath, videoPath); err != nil {
		return err
	}
	files := map[string]string{
		FileAudio:       audioPath,
		FileVideo:       videoPath,
		FileCover:       stillPath,
		FileSRT:         "",
		FileVTT:         "",
		FileDescription: "",
	}
	texts := map[string]string{
		FileSRT:         srt,
		FileVTT:         vtt,
		FileDescription: description,
	}
	out := zip.NewWriter(w)
	for _, name := range BundleOrder {
		entry, err := out.Create(name)
		if err != nil {
			_ = out.Close()
			return fmt.Errorf("export: bundle %s: %w", name, err)
		}
		if path := files[name]; path != "" {
			if err := copyFile(entry, path); err != nil {
				_ = out.Close()
				return fmt.Errorf("export: bundle %s: %w", name, err)
			}
			continue
		}
		if _, err := io.WriteString(entry, texts[name]); err != nil {
			_ = out.Close()
			return fmt.Errorf("export: bundle %s: %w", name, err)
		}
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("export: bundle: %w", err)
	}
	return nil
}

// copyFile streams one file onto w without holding it in memory. The
// video runs to tens of megabytes, so the bundle never buffers it.
func copyFile(w io.Writer, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(w, f)
	return err
}
