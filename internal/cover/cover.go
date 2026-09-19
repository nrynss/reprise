// Pass runs one cover job. It reserves budget first and refuses before
// any model call. The model draws from the title and show notes under
// the prompt constraints. A model failure, or art the validator
// rejects, still stores the deterministic fallback, so the episode
// ships a square cover either way. The pass makes no second attempt on
// any failure, so a restart never pays twice.

package cover

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/nrynss/keel/cost"
	"github.com/nrynss/keel/id"
	"github.com/nrynss/keel/job"
	"github.com/nrynss/reprise/internal/editorial"
)

// Kind names the job kind the wiring registers for this pass. One cover
// draws at a time. The kind is not idempotent, so a restart marks the
// job interrupted and the episode waits for an explicit retry. No
// resume path exists, because a resumed call would pay twice.
var Kind = job.Kind{Limit: 1}

// ErrInvalid reports a run with an empty model, owner, episode, store,
// budget, receipt sink, or directory.
var ErrInvalid = errors.New("cover: invalid argument")

// ErrGenerate reports a model call that failed before returning art.
// The wrapped error carries the cause. The run still stores the
// fallback, so this sentinel leaves the run only from the scripted
// double in tests that assert the failure reached the pass.
var ErrGenerate = errors.New("cover: generate failed")

// Model draws one episode cover. The production wiring binds it to the
// image endpoint once the credential route covers it. Tests bind the
// scripted double, so the pass runs offline.
type Model interface {
	// GenerateImage draws one square panel from the brief and returns
	// its PNG bytes.
	GenerateImage(ctx context.Context, model string, req ImageRequest) (ImageAnswer, error)
}

// ImageRequest carries one cover brief. Prompt is the full brief the
// pass built. Title, ShowNotes, and EpisodeNumber travel beside it so
// a future endpoint can weight them on its own.
type ImageRequest struct {
	// Prompt is the full image brief with its constraints.
	Prompt string
	// Title is the episode title the brief names.
	Title string
	// ShowNotes are the episode notes the brief names.
	ShowNotes string
	// EpisodeNumber identifies the episode the panel belongs to.
	EpisodeNumber int
}

// ImageAnswer carries one drawn panel as PNG bytes.
type ImageAnswer struct {
	// Image holds the panel as PNG bytes.
	Image []byte
}

// Budget bounds one paid call. A cost Budget satisfies it through the
// wiring adapter.
type Budget interface {
	// Reserve holds the estimate before the call and refuses when the
	// estimate would pass the limit.
	Reserve(cost.Price) error
	// Settle books the measured price and frees the reservation.
	Settle(reserved, actual cost.Price) error
	// Release frees a reservation the caller never spent.
	Release(cost.Price)
}

// RawSink persists the provider response bytes on receipt. The wiring
// points it at the episode media area, because the answer is the record
// of what the money bought.
type RawSink func(ctx context.Context, raw []byte) error

// Config carries one cover run.
type Config struct {
	// DB is the diary writer holding episodes and proposals.
	DB *sql.DB
	// Model draws the panel.
	Model Model
	// Budgets refuses the call when the owner or global ceiling is full.
	Budgets Budget
	// ModelID names the image model from settings, never from code.
	ModelID string
	// OwnerID scopes the episode read and the cover written.
	OwnerID string
	// EpisodeID scopes the cover written.
	EpisodeID string
	// Dir holds the directory that keeps the cover files.
	Dir string
	// SaveRaw persists the provider response on receipt.
	SaveRaw RawSink
	// Logger records rejected art. Nil means the default logger.
	Logger *slog.Logger
}

// Result reports one finished cover run.
type Result struct {
	// Fallback reports the model path failed and the deterministic
	// cover stands in.
	Fallback bool
	// File names the stored cover file inside Dir.
	File string
	// Width and Height measure the stored panel in pixels.
	Width  int
	Height int
	// SHA256 fingerprints the stored bytes.
	SHA256 string
	// Price is the settled model price for the call.
	Price cost.Price
}

// DollarsPerImage prices one image call. The caller reserves this
// before the provider call and settles it after. The figure is a
// placeholder until billing data lands.
const DollarsPerImage = 0.04

// Estimate prices one image call. The caller reserves this before the
// provider call.
func Estimate() cost.Price {
	return cost.USD(DollarsPerImage)
}

// Run draws one episode cover and stores it. It reserves budget first,
// and a refused reservation returns before any model call. A failed
// call or rejected art stores the fallback instead, and frees or
// settles the reservation depending on whether the call spent. Run
// makes no second attempt on any failure, so a restart never pays
// twice.
func Run(ctx context.Context, cfg Config) (Result, error) {
	if cfg.DB == nil || cfg.Model == nil || cfg.Budgets == nil {
		return Result{}, fmt.Errorf("cover: run: %w: missing store, model, or budget", ErrInvalid)
	}
	if cfg.ModelID == "" || cfg.OwnerID == "" || cfg.EpisodeID == "" || cfg.Dir == "" {
		return Result{}, fmt.Errorf("cover: run: %w: empty model, owner, episode, or directory", ErrInvalid)
	}
	if cfg.SaveRaw == nil {
		return Result{}, fmt.Errorf("cover: run: %w: missing receipt store", ErrInvalid)
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	if err := ensureSchema(ctx, cfg.DB); err != nil {
		return Result{}, err
	}
	number, err := loadEpisodeNumber(ctx, cfg.DB, cfg.EpisodeID)
	if err != nil {
		return Result{}, err
	}
	title, notes, err := loadBrief(ctx, cfg.DB, cfg.EpisodeID)
	if err != nil {
		return Result{}, err
	}
	estimate := Estimate()
	if err := cfg.Budgets.Reserve(estimate); err != nil {
		return Result{}, fmt.Errorf("cover: run: %w", err)
	}
	settled := false
	defer func() {
		if !settled {
			cfg.Budgets.Release(estimate)
		}
	}()
	req := ImageRequest{Prompt: Prompt(title, notes), Title: title, ShowNotes: notes, EpisodeNumber: number}
	answer, err := cfg.Model.GenerateImage(ctx, cfg.ModelID, req)
	if err != nil {
		log.Warn("cover: model failed, storing the fallback", "error", err)
		return storeFallback(ctx, cfg.DB, cfg.OwnerID, cfg.EpisodeID, cfg.Dir, number)
	}
	if err := cfg.SaveRaw(ctx, answer.Image); err != nil {
		if settleErr := cfg.Budgets.Settle(estimate, estimate); settleErr != nil {
			return Result{}, errors.Join(err, fmt.Errorf("cover: run: settle: %w", settleErr))
		}
		settled = true
		return Result{}, fmt.Errorf("cover: run: receipt store: %w", err)
	}
	width, height, err := Validate(answer.Image)
	if err != nil {
		log.Warn("cover: art rejected, storing the fallback", "error", err)
		if settleErr := cfg.Budgets.Settle(estimate, estimate); settleErr != nil {
			return Result{}, errors.Join(err, fmt.Errorf("cover: run: settle: %w", settleErr))
		}
		settled = true
		return storeFallback(ctx, cfg.DB, cfg.OwnerID, cfg.EpisodeID, cfg.Dir, number)
	}
	result, err := storeImage(ctx, cfg.DB, cfg.OwnerID, cfg.EpisodeID, cfg.Dir, answer.Image, width, height, false)
	if err != nil {
		if settleErr := cfg.Budgets.Settle(estimate, estimate); settleErr != nil {
			return Result{}, errors.Join(err, fmt.Errorf("cover: run: settle: %w", settleErr))
		}
		settled = true
		return Result{}, err
	}
	if err := cfg.Budgets.Settle(estimate, estimate); err != nil {
		return Result{}, fmt.Errorf("cover: run: settle: %w", err)
	}
	settled = true
	result.Price = estimate
	return result, nil
}

// storeFallback draws the deterministic panel and stores it. No model
// call precedes it on this path, so the caller releases the
// reservation through its deferred release.
func storeFallback(ctx context.Context, db *sql.DB, ownerID, episodeID, dir string, number int) (Result, error) {
	img := FallbackImage(number)
	if len(img) == 0 {
		return Result{}, fmt.Errorf("cover: fallback: %w: empty panel", ErrGenerate)
	}
	return storeImage(ctx, db, ownerID, episodeID, dir, img, Size, Size, true)
}

// storeImage writes one panel file and its cover row. The file name
// derives from the episode, so a rerun overwrites the same file and a
// repeated call stays safe.
func storeImage(ctx context.Context, db *sql.DB, ownerID, episodeID, dir string, img []byte, width, height int, fallback bool) (Result, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Result{}, fmt.Errorf("cover: store: %w", err)
	}
	name := episodeID + ".png"
	if err := os.WriteFile(filepath.Join(dir, name), img, 0o600); err != nil {
		return Result{}, fmt.Errorf("cover: store: %w", err)
	}
	sum := sha256.Sum256(img)
	sha := hex.EncodeToString(sum[:])
	coverID, err := id.New()
	if err != nil {
		return Result{}, fmt.Errorf("cover: store: %w", err)
	}
	fallbackFlag := 0
	if fallback {
		fallbackFlag = 1
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return Result{}, fmt.Errorf("cover: store: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM covers WHERE episode_id = ?", episodeID); err != nil {
		return Result{}, fmt.Errorf("cover: store: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO covers (id, owner_id, episode_id, file, sha256, width, height, fallback, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
		coverID, ownerID, episodeID, name, sha, width, height, fallbackFlag, time.Now().Unix()); err != nil {
		return Result{}, fmt.Errorf("cover: store: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Result{}, fmt.Errorf("cover: store: %w", err)
	}
	return Result{Fallback: fallback, File: name, Width: width, Height: height, SHA256: sha}, nil
}

// ensureSchema keeps the covers table beside the diary tables. The
// diary schema lives in its own namespace, so this table uses plain
// guarded statements instead of a second ledger.
func ensureSchema(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS covers (
    id TEXT NOT NULL PRIMARY KEY,
    owner_id TEXT NOT NULL REFERENCES users (id),
    episode_id TEXT NOT NULL REFERENCES episodes (id) ON DELETE CASCADE,
    file TEXT NOT NULL,
    sha256 TEXT NOT NULL,
    width INTEGER NOT NULL,
    height INTEGER NOT NULL,
    fallback INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    UNIQUE (episode_id)
)`); err != nil {
		return fmt.Errorf("cover: schema: %w", err)
	}
	if _, err := db.ExecContext(ctx, "CREATE INDEX IF NOT EXISTS covers_episode_idx ON covers (episode_id)"); err != nil {
		return fmt.Errorf("cover: schema: %w", err)
	}
	return nil
}

// loadEpisodeNumber reads the episode number the fallback draws from.
// A missing episode fails the run, because the fallback needs it.
func loadEpisodeNumber(ctx context.Context, db *sql.DB, episodeID string) (int, error) {
	var number int
	if err := db.QueryRowContext(ctx, "SELECT number FROM episodes WHERE id = ?", episodeID).Scan(&number); err != nil {
		return 0, fmt.Errorf("cover: load episode: %w", err)
	}
	return number, nil
}

// loadBrief reads the title and show notes proposals the editorial
// pass stored. A missing row reads empty, and the prompt carries what
// it found.
func loadBrief(ctx context.Context, db *sql.DB, episodeID string) (string, string, error) {
	read := func(kind string) (string, error) {
		var reason string
		err := db.QueryRowContext(ctx,
			"SELECT reason FROM proposals WHERE episode_id = ? AND kind = ? ORDER BY rowid DESC LIMIT 1",
			episodeID, kind).Scan(&reason)
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		if err != nil {
			return "", fmt.Errorf("cover: load brief: %w", err)
		}
		return reason, nil
	}
	title, err := read(editorial.KindTitle)
	if err != nil {
		return "", "", err
	}
	notes, err := read(editorial.KindShowNotes)
	if err != nil {
		return "", "", err
	}
	return title, notes, nil
}
