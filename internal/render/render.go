// Package render turns two stems and the editorial decisions into one episode.
//
// The edit model lives here. Accepted cuts name word offsets into the edit
// timeline, and this package converts them to millisecond ranges, removes
// each range from both stems together, and places the cold open first with
// a short gap before the episode. The same inputs always hash to the same
// name, so a repeated run reuses the stored output instead of rendering
// again. The render kind resumes safely after a restart for the same
// reason. Nothing here spends money, so no budget reservation precedes a
// run.
//
// The assembly runs in two stages over keel/ffmpeg. First both stems mix
// to one file at 48 kHz stereo with the alignment offsets applied. Then
// one filter graph cuts the kept ranges with short crossfades, splices the
// cold open and the gap ahead of the episode, and normalises loudness in
// two passes. The two-pass design mirrors the library renderer, but the
// graph here mixes two inputs, duplicates the cold open span, and aims at
// the episode targets, which that single-input call cannot express.
//
// Opus serves streaming and AAC serves export. Both persist private, so
// only the owner reads them until an explicit publish.
package render

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/nrynss/keel/ffmpeg"
	"github.com/nrynss/keel/mediastore"
)

// Targets the assembly aims at. Integrated loudness and true peak follow
// the episode spec. The loudness range stays at the tool default, because
// the spec names no range.
const (
	// TargetLUFS names the integrated loudness in LUFS.
	TargetLUFS = -16.0
	// TruePeakDBTP caps the true peak in dBTP.
	TruePeakDBTP = -1.0
	// LoudnessRangeLU names the loudness range in LU.
	LoudnessRangeLU = 7.0
)

// Assembly constants. Every join between two kept ranges carries the
// crossfade when both neighbours last at least that long, and a hard cut
// otherwise. The gap separates the cold open from the episode body.
const (
	// Crossfade is the fade length at a join.
	Crossfade = 10 * time.Millisecond
	// ColdOpenGap is the silence between the cold open and the episode.
	ColdOpenGap = 750 * time.Millisecond
	// MixRate is the sample rate of the intermediate mix.
	MixRate = 48000
)

// Output content types carried to the media store. Opus streams inside
// Ogg. AAC exports inside MP4, so the store wiring admits that type.
const (
	// OpusContentType serves the streaming render.
	OpusContentType = "audio/ogg"
	// AACContentType serves the export render.
	AACContentType = "audio/mp4"
)

// Sentinel errors. Every failure path this package produces wraps one of
// these, so callers branch with errors.Is.
var (
	// ErrInvalid reports a call with an empty id, a nil dependency, or an
	// unusable path.
	ErrInvalid = errors.New("render: invalid argument")
	// ErrEmpty reports inputs whose accepted cuts remove every kept
	// range, so no audio remains to assemble.
	ErrEmpty = errors.New("render: cuts remove all audio")
	// ErrNoStems reports a resolver that returns no stem path.
	ErrNoStems = errors.New("render: stem audio missing")
	// ErrNoMeasurement reports a measure pass with no usable stats.
	ErrNoMeasurement = errors.New("render: loudness measurement missing")
	// ErrInterrupted reports a run whose context ended first. A rerun
	// with the same inputs reuses or rebuilds the same hash.
	ErrInterrupted = errors.New("render: interrupted")
)

// Media persists finished renders and reads them back. The library
// store writes bytes while its index answers reads, so this seam takes
// both behind one interface. Tests bind fakes here without touching
// disk layout.
type Media interface {
	// Persist stores src under ContentType and returns its blob id.
	Persist(ctx context.Context, src io.Reader, p mediastore.Put) (string, error)
	// Get returns the blob stored under id, or a not-found error.
	Get(ctx context.Context, id string) (mediastore.Blob, error)
}

// BlobIndex answers blob reads. A media index store satisfies it
// directly.
type BlobIndex interface {
	// Get returns the blob stored under id, or a not-found error.
	Get(ctx context.Context, id string) (mediastore.Blob, error)
}

// StoreMedia binds a media store to its index behind the Media seam.
// The wiring builds one value per process and hands it to the resolver.
func StoreMedia(store *mediastore.Store, index BlobIndex) Media {
	return &storeMedia{store: store, index: index}
}

// storeMedia carries the write side and the read side together.
type storeMedia struct {
	// store persists blob bytes.
	store *mediastore.Store
	// index answers blob reads.
	index BlobIndex
}

// Persist stores src through the media store.
func (m *storeMedia) Persist(ctx context.Context, src io.Reader, p mediastore.Put) (string, error) {
	if m == nil || m.store == nil {
		return "", fmt.Errorf("render: persist: %w", ErrInvalid)
	}
	return m.store.Persist(ctx, src, p)
}

// Get reads blob metadata through the index.
func (m *storeMedia) Get(ctx context.Context, id string) (mediastore.Blob, error) {
	if m == nil || m.index == nil {
		return mediastore.Blob{}, fmt.Errorf("render: get blob: %w", ErrInvalid)
	}
	return m.index.Get(ctx, id)
}

// Tools names the ffmpeg and ffprobe executables a run invokes. It
// aliases the library type so wiring passes its tools straight through.
type Tools = ffmpeg.Tools

// Result reports one finished render.
type Result struct {
	// Hash names the inputs. Equal inputs share it.
	Hash string
	// OpusMediaID is the streaming blob id.
	OpusMediaID string
	// AACMediaID is the export blob id.
	AACMediaID string
	// Loudness is the measured integrated loudness in LUFS.
	Loudness float64
	// Reused reports the run found the stored output and rendered
	// nothing.
	Reused bool
}
