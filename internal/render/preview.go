package render

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nrynss/keel/ffmpeg"
	"github.com/nrynss/keel/id"
	"github.com/nrynss/keel/job"
)

// PreviewResult reports one finished preview.
type PreviewResult struct {
	// Hash names the inputs. Equal inputs share it.
	Hash string
	// MediaID is the preview opus blob id.
	MediaID string
	// Reused reports the run found the stored output and rendered
	// nothing.
	Reused bool
}

// StoredPreview is one preview row the same inputs produced before.
type StoredPreview struct {
	// Hash names the inputs that produced it.
	Hash string
	// MediaID is the preview opus blob id.
	MediaID string
}

// previewAttempts caps the preview attempts a restart may make. The
// preview calls nothing paid and names its output by its inputs, so a
// resumed attempt lands on the same file. The cap still stops a preview
// that dies every time from resuming forever.
const previewAttempts = 3

// PreviewKindOf registers the preview kind with room for concurrency
// previews at once. The value follows the render concurrency setting,
// because the preview runs the same ffmpeg mix on the same machine. A
// value below one returns ErrConcurrency instead of a kind, so the boot
// stops before the runner opens. The kind is idempotent with room for a
// resumed attempt, so a restart resumes through the kind alone. The
// preview calls nothing paid, so it stays out of the paid set and never
// marks its episode interrupted. The output name hashes the inputs, so
// the resumed run reuses or rebuilds the same bytes.
func PreviewKindOf(r *Resolver, concurrency int) (job.Kind, error) {
	if concurrency < 1 {
		return job.Kind{}, fmt.Errorf("%w, got %d", ErrConcurrency, concurrency)
	}
	return job.Kind{Limit: concurrency, Idempotent: true, MaxAttempts: previewAttempts, Resume: r.ResumePreview}, nil
}

// PreviewFunc builds the job work for one episode. It publishes the
// descriptor first, so an interruption still leaves the record carrying
// enough to resume. Every later report carries the same linkage,
// because a report replaces the stored snapshot whole and a bare stage
// would otherwise drop the mapping the schedule reads back. The
// terminal data carries the result as JSON.
func (r *Resolver) PreviewFunc(ownerID, episodeID string) job.Func {
	return func(ctx context.Context, progress func(job.Progress)) ([]byte, error) {
		desc := descriptor{OwnerID: ownerID, EpisodeID: episodeID}
		raw, err := json.Marshal(desc)
		if err != nil {
			return nil, fmt.Errorf("render: describe preview work: %w", err)
		}
		progress(job.Progress{Stage: "start", Detail: raw})
		linked := func(p job.Progress) {
			p.Detail = raw
			progress(p)
		}
		res, err := r.Preview(ctx, ownerID, episodeID, linked)
		if err != nil {
			return nil, err
		}
		out, err := json.Marshal(res)
		if err != nil {
			return nil, fmt.Errorf("render: encode preview result: %w", err)
		}
		return out, nil
	}
}

// ResumePreview rebuilds the work for an interrupted preview. It decodes
// the descriptor the first attempt published, then reruns the episode.
// Equal inputs hash equal, so the resumed run lands on the same output.
func (r *Resolver) ResumePreview(rec job.Record) (job.Func, error) {
	if r == nil {
		return nil, fmt.Errorf("render: resume preview: %w", ErrInvalid)
	}
	var desc descriptor
	if err := json.Unmarshal(rec.Progress.Detail, &desc); err != nil {
		return nil, fmt.Errorf("render: resume preview: %w", err)
	}
	if desc.OwnerID == "" || desc.EpisodeID == "" {
		return nil, fmt.Errorf("render: resume preview: %w", ErrInvalid)
	}
	return r.PreviewFunc(desc.OwnerID, desc.EpisodeID), nil
}

// Preview mixes one episode preview. It locates the stems and mixes
// them through the same steps the render uses, with the same offsets,
// so the stored word times land on the preview unchanged. It applies no
// cuts and no cold open. It hashes the stems and the offsets, then
// reuses the stored output when the same inputs previewed before.
// Otherwise it mixes, encodes one opus file, persists it private, and
// records the preview row.
func (r *Resolver) Preview(ctx context.Context, ownerID, episodeID string, progress func(job.Progress)) (PreviewResult, error) {
	if r == nil || r.DB == nil || r.Media == nil || r.Locate == nil || r.WorkDir == "" {
		return PreviewResult{}, fmt.Errorf("render: preview: %w", ErrInvalid)
	}
	if ownerID == "" || episodeID == "" {
		return PreviewResult{}, fmt.Errorf("render: preview: %w", ErrInvalid)
	}
	report := func(stage string) {
		if progress != nil {
			progress(job.Progress{Stage: stage})
		}
	}
	if ctx.Err() != nil {
		return PreviewResult{}, fmt.Errorf("render: preview: %w: %v", ErrInterrupted, ctx.Err())
	}
	userPath, hostPath, userOffsetMs, hostOffsetMs, err := r.Locate(ctx, ownerID, episodeID)
	if err != nil {
		return PreviewResult{}, fmt.Errorf("render: preview locate stems: %w", err)
	}
	if userPath == "" || hostPath == "" {
		return PreviewResult{}, fmt.Errorf("render: preview locate stems: %w", ErrNoStems)
	}
	userDigest, err := digestFile(userPath)
	if err != nil {
		return PreviewResult{}, fmt.Errorf("render: preview digest user stem: %w", err)
	}
	hostDigest, err := digestFile(hostPath)
	if err != nil {
		return PreviewResult{}, fmt.Errorf("render: preview digest host stem: %w", err)
	}
	hash := HashPreviewInputs(userDigest, hostDigest, userOffsetMs, hostOffsetMs)
	if found, err := FindPreview(ctx, r.DB, episodeID, hash); err != nil {
		return PreviewResult{}, err
	} else if found != nil && r.previewBlobPresent(ctx, found) {
		return PreviewResult{Hash: found.Hash, MediaID: found.MediaID, Reused: true}, nil
	}

	report("mix")
	mixPath := filepath.Join(r.WorkDir, "preview-mix-"+hash+".wav")
	if err := mixStems(ctx, r.Tools, userPath, userOffsetMs, hostPath, hostOffsetMs, mixPath); err != nil {
		return PreviewResult{}, err
	}
	defer os.Remove(mixPath)

	report("encode")
	opusPath := filepath.Join(r.WorkDir, "preview-"+hash+".opus")
	if err := encodePreview(ctx, r.Tools, mixPath, opusPath); err != nil {
		return PreviewResult{}, err
	}
	defer os.Remove(opusPath)

	report("store")
	opusID, err := r.persist(ctx, ownerID, episodeID, opusPath, OpusContentType)
	if err != nil {
		return PreviewResult{}, err
	}
	res := PreviewResult{Hash: hash, MediaID: opusID}
	if err := StorePreview(ctx, r.DB, ownerID, episodeID, res); err != nil {
		return PreviewResult{}, err
	}
	report("done")
	return res, nil
}

// previewBlobPresent reports whether the stored preview blob still
// reads. A missing blob rebuilds instead of handing back an id that
// serves nothing.
func (r *Resolver) previewBlobPresent(ctx context.Context, found *StoredPreview) bool {
	if _, err := r.Media.Get(ctx, found.MediaID); err != nil {
		return false
	}
	return true
}

// HashPreviewInputs names the preview for its inputs. The mix carries
// the rate, the denoiser setting and the encoder setting, so each names
// the output beside the stem bytes and the offsets.
func HashPreviewInputs(userSHA256, hostSHA256 [32]byte, userOffsetMs, hostOffsetMs int64) string {
	var b strings.Builder
	fmt.Fprintf(&b, "preview user:%x\n", userSHA256)
	fmt.Fprintf(&b, "preview host:%x\n", hostSHA256)
	fmt.Fprintf(&b, "preview offsets:%d:%d\n", userOffsetMs, hostOffsetMs)
	fmt.Fprintf(&b, "preview mixrate:%d\n", MixRate)
	fmt.Fprintf(&b, "preview denoise:%d\n", DenoiseNR)
	fmt.Fprintf(&b, "preview opus:%s\n", OpusBitrate)
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// encodePreview writes the mix to one opus file. The preview is a draft
// listen, so one encode pass without the loudness normalisation is
// enough. The render keeps its own two-pass encode for the episode.
func encodePreview(ctx context.Context, tools ffmpeg.Tools, mixPath, opusDst string) error {
	if ctx.Err() != nil {
		return fmt.Errorf("render: preview encode: %w", ctx.Err())
	}
	args := []string{"-y", "-i", mixPath, "-c:a", "libopus", "-b:a", OpusBitrate, opusDst}
	if err := ffmpeg.Run(ctx, tools, args...); err != nil {
		return fmt.Errorf("render: preview encode: %w", err)
	}
	return nil
}

// ensurePreviewTable creates the preview table the way the diary schema
// creates its own tables. The render package owns this table, so no
// diary migration carries it. The episode delete cascades to its preview
// rows, so an erased episode leaves no preview behind.
func ensurePreviewTable(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("render: preview table: %w", ErrInvalid)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS previews (
    id TEXT NOT NULL PRIMARY KEY,
    owner_id TEXT NOT NULL REFERENCES users (id),
    episode_id TEXT NOT NULL REFERENCES episodes (id) ON DELETE CASCADE,
    input_hash TEXT NOT NULL,
    media_id TEXT NOT NULL
)`); err != nil {
		return fmt.Errorf("render: preview table: %w", err)
	}
	if _, err := db.ExecContext(ctx,
		`CREATE INDEX IF NOT EXISTS previews_episode_idx ON previews (episode_id)`); err != nil {
		return fmt.Errorf("render: preview table: %w", err)
	}
	return nil
}

// FindPreview returns the stored preview for the input hash, or nil when
// no run with these inputs finished yet.
func FindPreview(ctx context.Context, db *sql.DB, episodeID, hash string) (*StoredPreview, error) {
	if db == nil || episodeID == "" || hash == "" {
		return nil, fmt.Errorf("render: find preview: %w", ErrInvalid)
	}
	if err := ensurePreviewTable(ctx, db); err != nil {
		return nil, err
	}
	var found StoredPreview
	err := db.QueryRowContext(ctx,
		`SELECT input_hash, media_id FROM previews
		 WHERE episode_id = ? AND input_hash = ? ORDER BY rowid DESC LIMIT 1`,
		episodeID, hash).Scan(&found.Hash, &found.MediaID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("render: find preview: %w", err)
	}
	return &found, nil
}

// StorePreview records one finished preview. A rerun with the same
// inputs finds the row through FindPreview instead of writing again.
func StorePreview(ctx context.Context, db *sql.DB, ownerID, episodeID string, res PreviewResult) error {
	if db == nil || ownerID == "" || episodeID == "" || res.Hash == "" {
		return fmt.Errorf("render: store preview: %w", ErrInvalid)
	}
	if res.MediaID == "" {
		return fmt.Errorf("render: store preview: %w", ErrInvalid)
	}
	if err := ensurePreviewTable(ctx, db); err != nil {
		return err
	}
	previewID, err := id.New()
	if err != nil {
		return fmt.Errorf("render: store preview: %w", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO previews (id, owner_id, episode_id, input_hash, media_id)
		 VALUES (?, ?, ?, ?, ?)`,
		previewID, ownerID, episodeID, res.Hash, res.MediaID); err != nil {
		return fmt.Errorf("render: store preview: %w", err)
	}
	return nil
}

// PreviewMediaID returns the newest preview opus media id for an episode
// the owner holds. Newest is the highest row id. It returns empty when
// no preview exists.
func PreviewMediaID(ctx context.Context, db *sql.DB, ownerID, episodeID string) (string, error) {
	if db == nil || ownerID == "" || episodeID == "" {
		return "", fmt.Errorf("render: preview media %q: %w", episodeID, ErrInvalid)
	}
	if err := ensurePreviewTable(ctx, db); err != nil {
		return "", err
	}
	var mediaID string
	err := db.QueryRowContext(ctx,
		`SELECT media_id FROM previews
		 WHERE episode_id = ? AND owner_id = ? AND media_id != ''
		 ORDER BY rowid DESC LIMIT 1`,
		episodeID, ownerID).Scan(&mediaID)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("render: preview media %q: %w", episodeID, err)
	}
	return mediaID, nil
}

// PreviewStore binds a database behind the preview read seam the detail
// handler declares. Create it with the diary writer, because the table
// creation runs beside the reads.
type PreviewStore struct {
	// DB is the diary writer holding the preview rows.
	DB *sql.DB
}

// PreviewMediaID returns the newest preview opus media id for an episode
// the owner holds, or empty when no preview exists.
func (s PreviewStore) PreviewMediaID(ctx context.Context, ownerID, episodeID string) (string, error) {
	if s.DB == nil {
		return "", fmt.Errorf("render: preview media %q: %w", episodeID, ErrInvalid)
	}
	return PreviewMediaID(ctx, s.DB, ownerID, episodeID)
}
