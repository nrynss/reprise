package render

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/nrynss/keel/ffmpeg"
	"github.com/nrynss/keel/job"
	"github.com/nrynss/keel/mediastore"
)

// Stems locates the two stem files for one episode with their alignment
// offsets. The wiring serves it from the stems rows and the media
// directory, and tests bind fixture paths behind the same seam.
type Stems func(ctx context.Context, ownerID, episodeID string) (userPath, hostPath string, userOffsetMs, hostOffsetMs int64, err error)

// Resolver carries everything a render needs. The wiring builds one and
// registers its kind, so every render job resolves stems, tools, media
// and storage the same way.
type Resolver struct {
	// DB is the diary writer holding words, proposals, and renders.
	DB *sql.DB
	// Tools names the ffmpeg and ffprobe executables.
	Tools ffmpeg.Tools
	// Media persists the finished outputs.
	Media Media
	// WorkDir holds the intermediate mix and outputs. It must exist.
	WorkDir string
	// Locate finds the stem files for an episode.
	Locate Stems
}

// descriptor travels in the job progress detail, so a resume after a
// restart rebuilds the same work from the interrupted record.
type descriptor struct {
	// OwnerID scopes the words read and the render written.
	OwnerID string `json:"owner_id"`
	// EpisodeID scopes the render.
	EpisodeID string `json:"episode_id"`
}

// KindOf registers the render kind. One render runs at a time, because
// two loudness passes already saturate one encoder. The kind is
// idempotent, so a restart resumes through the resolver instead of
// failing the episode. The output name hashes the inputs, so the resumed
// run reuses or rebuilds the same bytes.
func KindOf(r *Resolver) job.Kind {
	return job.Kind{Limit: 1, Idempotent: true, Resume: r.Resume}
}

// Func builds the job work for one episode. It publishes the descriptor
// first, so an interruption still leaves the record carrying enough to
// resume. The terminal data carries the result as JSON.
func (r *Resolver) Func(ownerID, episodeID string) job.Func {
	return func(ctx context.Context, progress func(job.Progress)) ([]byte, error) {
		raw, err := json.Marshal(descriptor{OwnerID: ownerID, EpisodeID: episodeID})
		if err != nil {
			return nil, fmt.Errorf("render: describe work: %w", err)
		}
		progress(job.Progress{Stage: "start", Detail: raw})
		res, err := r.Run(ctx, ownerID, episodeID, progress)
		if err != nil {
			return nil, err
		}
		out, err := json.Marshal(res)
		if err != nil {
			return nil, fmt.Errorf("render: encode result: %w", err)
		}
		return out, nil
	}
}

// Resume rebuilds the work for an interrupted render. It decodes the
// descriptor the first attempt published, then reruns the episode. Equal
// inputs hash equal, so the resumed run lands on the same output.
func (r *Resolver) Resume(rec job.Record) (job.Func, error) {
	if r == nil {
		return nil, fmt.Errorf("render: resume: %w", ErrInvalid)
	}
	var desc descriptor
	if err := json.Unmarshal(rec.Progress.Detail, &desc); err != nil {
		return nil, fmt.Errorf("render: resume: %w", err)
	}
	if desc.OwnerID == "" || desc.EpisodeID == "" {
		return nil, fmt.Errorf("render: resume: %w", ErrInvalid)
	}
	return r.Func(desc.OwnerID, desc.EpisodeID), nil
}

// Run renders one episode. It hashes the stems, the offsets, the accepted
// cuts and the cold open, then reuses the stored output when the same
// inputs rendered before. Otherwise it mixes, assembles, normalises,
// persists both outputs private, and records the render row.
func (r *Resolver) Run(ctx context.Context, ownerID, episodeID string, progress func(job.Progress)) (Result, error) {
	if r == nil || r.DB == nil || r.Media == nil || r.Locate == nil || r.WorkDir == "" {
		return Result{}, fmt.Errorf("render: run: %w", ErrInvalid)
	}
	if ownerID == "" || episodeID == "" {
		return Result{}, fmt.Errorf("render: run: %w", ErrInvalid)
	}
	report := func(stage string) {
		if progress != nil {
			progress(job.Progress{Stage: stage})
		}
	}
	if ctx.Err() != nil {
		return Result{}, fmt.Errorf("render: run: %w: %v", ErrInterrupted, ctx.Err())
	}
	userPath, hostPath, userOffsetMs, hostOffsetMs, err := r.Locate(ctx, ownerID, episodeID)
	if err != nil {
		return Result{}, fmt.Errorf("render: locate stems: %w", err)
	}
	if userPath == "" || hostPath == "" {
		return Result{}, fmt.Errorf("render: locate stems: %w", ErrNoStems)
	}
	in, err := r.describe(ctx, episodeID, userPath, hostPath, userOffsetMs, hostOffsetMs)
	if err != nil {
		return Result{}, err
	}
	hash := HashInputs(in)
	if found, err := FindRender(ctx, r.DB, episodeID, hash); err != nil {
		return Result{}, err
	} else if found != nil && r.blobsPresent(ctx, found) {
		return Result{
			Hash:        found.Hash,
			OpusMediaID: found.OpusMediaID,
			AACMediaID:  found.AACMediaID,
			Loudness:    found.Loudness,
			Reused:      true,
		}, nil
	}

	kept := KeepRanges(in.DurationMs, in.Cuts)
	if len(kept) == 0 {
		return Result{}, fmt.Errorf("render: run %s: %w", hash, ErrEmpty)
	}
	var cold []RangeMs
	if in.ColdOpen != nil {
		cold = IntersectRanges(*in.ColdOpen, kept)
	}
	planned := planAssembly(kept, cold)
	if planned.graph == "" {
		return Result{}, fmt.Errorf("render: run %s: %w", hash, ErrEmpty)
	}

	report("mix")
	mixPath := filepath.Join(r.WorkDir, "mix-"+hash+".wav")
	if err := mixStems(ctx, r.Tools, userPath, userOffsetMs, hostPath, hostOffsetMs, mixPath); err != nil {
		return Result{}, err
	}
	defer os.Remove(mixPath)

	report("measure")
	measured, err := measureLoudness(ctx, r.Tools, mixPath, planned.graph)
	if err != nil {
		return Result{}, err
	}

	report("encode")
	opusPath := filepath.Join(r.WorkDir, "render-"+hash+".opus")
	aacPath := filepath.Join(r.WorkDir, "render-"+hash+".m4a")
	if err := encodeOutputs(ctx, r.Tools, mixPath, planned.graph, measured, opusPath, aacPath); err != nil {
		return Result{}, err
	}
	defer os.Remove(opusPath)
	defer os.Remove(aacPath)

	report("verify")
	loudness, err := verifyLoudness(ctx, r.Tools, opusPath)
	if err != nil {
		return Result{}, err
	}

	report("store")
	opusID, err := r.persist(ctx, ownerID, episodeID, opusPath, OpusContentType)
	if err != nil {
		return Result{}, err
	}
	aacID, err := r.persist(ctx, ownerID, episodeID, aacPath, AACContentType)
	if err != nil {
		return Result{}, err
	}
	res := Result{Hash: hash, OpusMediaID: opusID, AACMediaID: aacID, Loudness: loudness}
	if err := StoreRender(ctx, r.DB, ownerID, episodeID, res); err != nil {
		return Result{}, err
	}
	report("done")
	return res, nil
}

// describe reads the database decisions and digests the stem files into
// one input. The hash over it names the render.
func (r *Resolver) describe(ctx context.Context, episodeID, userPath, hostPath string, userOffsetMs, hostOffsetMs int64) (Input, error) {
	cuts, err := AcceptedCuts(ctx, r.DB, episodeID)
	if err != nil {
		return Input{}, err
	}
	cold, err := ColdOpen(ctx, r.DB, episodeID)
	if err != nil {
		return Input{}, err
	}
	userDigest, err := digestFile(userPath)
	if err != nil {
		return Input{}, fmt.Errorf("render: digest user stem: %w", err)
	}
	hostDigest, err := digestFile(hostPath)
	if err != nil {
		return Input{}, fmt.Errorf("render: digest host stem: %w", err)
	}
	userDur, err := ffmpeg.Duration(ctx, r.Tools, userPath)
	if err != nil {
		return Input{}, fmt.Errorf("render: probe user stem: %w", err)
	}
	hostDur, err := ffmpeg.Duration(ctx, r.Tools, hostPath)
	if err != nil {
		return Input{}, fmt.Errorf("render: probe host stem: %w", err)
	}
	in := Input{
		UserSHA256:   userDigest,
		HostSHA256:   hostDigest,
		UserOffsetMs: userOffsetMs,
		HostOffsetMs: hostOffsetMs,
		Cuts:         toRanges(cuts),
		ColdOpen:     cold,
		DurationMs:   max(userOffsetMs+userDur.Milliseconds(), hostOffsetMs+hostDur.Milliseconds()),
	}
	return in, nil
}

// blobsPresent reports whether both stored blobs still read. A missing
// blob rerenders instead of handing back an id that serves nothing.
func (r *Resolver) blobsPresent(ctx context.Context, found *StoredRender) bool {
	if _, err := r.Media.Get(ctx, found.OpusMediaID); err != nil {
		return false
	}
	if _, err := r.Media.Get(ctx, found.AACMediaID); err != nil {
		return false
	}
	return true
}

// persist stores one finished file as a private blob grouped by episode,
// so the retention sweep evicts an episode as a unit. The file stays on
// disk until the persist succeeds, and the caller removes it after.
func (r *Resolver) persist(ctx context.Context, ownerID, episodeID, path, contentType string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("render: persist %s: %w", contentType, err)
	}
	defer f.Close()
	blobID, err := r.Media.Persist(ctx, f, mediastore.Put{
		ContentType: contentType,
		Owner:       ownerID,
		Group:       episodeID,
		Visibility:  mediastore.Private,
	})
	if err != nil {
		return "", fmt.Errorf("render: persist %s: %w", contentType, err)
	}
	return blobID, nil
}

// digestFile hashes one stem file. Equal bytes hash equal whatever the
// file name says, so a reupload under another name still reuses.
func digestFile(path string) ([32]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return [32]byte{}, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return [32]byte{}, fmt.Errorf("hash %s: %w", path, err)
	}
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum, nil
}

// toRanges converts accepted cuts to episode-clock ranges.
func toRanges(cuts []Cut) []RangeMs {
	out := make([]RangeMs, 0, len(cuts))
	for _, c := range cuts {
		out = append(out, RangeMs{Start: c.StartMs, End: c.EndMs})
	}
	return out
}
