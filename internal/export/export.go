// Package export bundles one finished episode for upload.
//
// The bundle holds exactly what the upload form asks for. The AAC render
// is the audio master. A still cover plus that audio becomes a 1920 by
// 1080 video with the waveform drawn over the still. The rendered words
// become SRT and WebVTT captions through the library captioner. The show
// notes plus timestamped chapter lines become the description. The square
// cover art travels as is.
//
// This package supplies word timings and the still. It writes neither
// caption format itself. The media store already accepts both caption
// types, so the wiring persists these bytes unchanged.
package export

import (
	"errors"
	"time"
)

// Frame size of the upload video. The output stores colour in pixel
// pairs, so both values stay even.
const (
	// Width is the video frame width in pixels.
	Width = 1920
	// Height is the video frame height in pixels.
	Height = 1080
)

// Chapter rules the description enforces. The upload form turns
// timestamped lines into markers only when the first opens at zero,
// at least three lines exist, and every chapter lasts ten seconds.
const (
	// MinChapters is the smallest chapter count the form accepts.
	MinChapters = 3
	// MinChapterMs is the shortest chapter the form keeps, in milliseconds.
	MinChapterMs = int64(10 * time.Second / time.Millisecond)
)

// Blob content types the wiring persists each bundle file under.
const (
	// AudioContentType serves the AAC render inside MP4.
	AudioContentType = "audio/mp4"
	// VideoContentType serves the waveform video.
	VideoContentType = "video/mp4"
	// SRTContentType serves the SubRip captions.
	SRTContentType = "application/x-subrip"
	// VTTContentType serves the WebVTT captions.
	VTTContentType = "text/vtt"
	// CoverContentType serves the square cover art.
	CoverContentType = "image/png"
)

// Sentinel errors. Every failure path this package produces wraps one of
// these, so callers branch with errors.Is.
var (
	// ErrInvalid reports a call with an empty id, a nil dependency, or
	// an unusable path.
	ErrInvalid = errors.New("export: invalid argument")
	// ErrNoWords reports a caption request with no words to group.
	ErrNoWords = errors.New("export: no words")
	// ErrOrder reports a word ending before it starts, or words out of
	// timeline order.
	ErrOrder = errors.New("export: word out of order")
	// ErrChapters reports chapters the upload form would ignore, such as
	// none at all, a first chapter past zero, too few chapters, or a
	// span under the minimum length.
	ErrChapters = errors.New("export: unusable chapters")
)

// Word is one rendered word with its timing on the episode clock. The
// render starts at zero, so these offsets match the audio the captions
// travel with.
type Word struct {
	// Text is the word as heard, with its original casing.
	Text string
	// StartMs is the word start in milliseconds from the render start.
	StartMs int64
	// EndMs is the word end in milliseconds from the render start.
	EndMs int64
}

// Chapter is one episode chapter with its start on the episode clock.
// Every start past zero matches a rendered word start, so seeking to a
// description line lands on speech.
type Chapter struct {
	// Title is the chapter title.
	Title string
	// StartMs is the chapter start in milliseconds from the render start.
	StartMs int64
}
