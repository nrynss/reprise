package editorial

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"math"

	"github.com/nrynss/keel/cost"
	"github.com/nrynss/keel/job"
	"github.com/nrynss/reprise/internal/gemini"
)

// Kind names the job kind the wiring registers for this pass. Two
// editorial passes run at once. The kind is not idempotent, so a restart
// marks the job interrupted and the episode waits for an explicit retry.
// No resume path exists, because a resumed call would pay for the same
// audio twice.
var Kind = job.Kind{Limit: 2}

// Model proposes the episode. The Gemini client satisfies it. Tests bind
// a scripted answer, so the pass runs offline.
type Model interface {
	// GenerateEditorial hears both stems, reads the timeline, and returns
	// the proposals as JSON with its token usage.
	GenerateEditorial(ctx context.Context, model string, req gemini.EditorialRequest) (gemini.EditorialAnswer, error)
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

// Config carries one editorial run.
type Config struct {
	// DB is the diary writer holding words, proposals, and callbacks.
	DB *sql.DB
	// Model hears the stems and proposes the episode.
	Model Model
	// Budgets refuses the call when the owner or global ceiling is full.
	Budgets Budget
	// ModelID names the editorial model from settings, never from code.
	ModelID string
	// OwnerID scopes the words read and the proposals written.
	OwnerID string
	// EpisodeID scopes the proposals written.
	EpisodeID string
	// UserStem holds the speaker stem audio.
	UserStem []byte
	// HostStem holds the host stem audio.
	HostStem []byte
	// DurationSecs prices the call at the audio minute rate.
	DurationSecs float64
	// SaveRaw persists the provider response on receipt.
	SaveRaw RawSink
	// Logger records dropped proposals. Nil means the default logger.
	Logger *slog.Logger
}

// Result reports one finished editorial pass.
type Result struct {
	// Fallback reports the model failed and the draft is renderable with
	// no cuts and a plain title.
	Fallback bool
	// Title is the episode title the pass stored.
	Title string
	// Cuts counts the accepted cut proposals stored.
	Cuts int
	// Price is the settled model price for the audio seconds.
	Price cost.Price
}

// DollarsPerMinute prices one audio minute of the editorial call. The
// caller reserves this before the provider call and settles the measured
// audio minutes after.
const DollarsPerMinute = 0.0025

// Estimate prices audio seconds at the editorial minute rate, rounded up
// to the nanodollar. The caller reserves this before the provider call.
func Estimate(audioSeconds float64) cost.Price {
	if audioSeconds <= 0 {
		return cost.Price(0)
	}
	perSecond := float64(cost.USD(DollarsPerMinute)) / 60
	return cost.Price(math.Ceil(perSecond * audioSeconds))
}

// Run hears both stems, validates every proposal against the word
// timeline, and stores the draft. It reserves budget first, and a refused
// reservation returns before any provider call. Dangling proposals are
// dropped and logged, never stored. A model failure still leaves a
// renderable draft with no cuts and a plain title, and reports it through
// Result. Run makes no second attempt on any failure, so a restart never
// pays twice.
func Run(ctx context.Context, cfg Config) (Result, error) {
	if cfg.DB == nil || cfg.Model == nil || cfg.Budgets == nil {
		return Result{}, fmt.Errorf("editorial: run: %w: missing store, model, or budget", ErrInvalid)
	}
	if cfg.ModelID == "" || cfg.OwnerID == "" || cfg.EpisodeID == "" {
		return Result{}, fmt.Errorf("editorial: run: %w: empty model, owner, or episode", ErrInvalid)
	}
	if len(cfg.UserStem) == 0 || len(cfg.HostStem) == 0 || cfg.DurationSecs <= 0 {
		return Result{}, fmt.Errorf("editorial: run: %w: empty audio or duration", ErrInvalid)
	}
	if cfg.SaveRaw == nil {
		return Result{}, fmt.Errorf("editorial: run: %w: missing receipt store", ErrInvalid)
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	words, err := loadWords(ctx, cfg.DB, cfg.EpisodeID)
	if err != nil {
		return Result{}, err
	}
	estimate := Estimate(cfg.DurationSecs)
	if err := cfg.Budgets.Reserve(estimate); err != nil {
		return Result{}, fmt.Errorf("editorial: run: %w", err)
	}
	settled := false
	defer func() {
		if !settled {
			cfg.Budgets.Release(estimate)
		}
	}()
	reply, err := cfg.Model.GenerateEditorial(ctx, cfg.ModelID, gemini.EditorialRequest{
		UserStem: cfg.UserStem,
		HostStem: cfg.HostStem,
		Timeline: timeline(words),
	})
	if err != nil {
		log.Warn("editorial: model failed, leaving a renderable draft", "error", err)
		if storeErr := store(ctx, cfg.DB, cfg.OwnerID, cfg.EpisodeID, words, draft{Title: plainTitle(len(words))}); storeErr != nil {
			return Result{}, storeErr
		}
		return Result{Fallback: true, Title: plainTitle(len(words)), Price: cost.Price(0)}, nil
	}
	if err := cfg.SaveRaw(ctx, []byte(reply.JSON)); err != nil {
		return Result{}, fmt.Errorf("editorial: run: receipt store: %w", err)
	}
	got, err := decode(reply.JSON)
	if err != nil {
		log.Warn("editorial: answer unusable, leaving a renderable draft", "error", err)
		if storeErr := store(ctx, cfg.DB, cfg.OwnerID, cfg.EpisodeID, words, draft{Title: plainTitle(len(words))}); storeErr != nil {
			return Result{}, storeErr
		}
		if settleErr := cfg.Budgets.Settle(estimate, estimate); settleErr != nil {
			return Result{}, fmt.Errorf("editorial: run: settle: %w", settleErr)
		}
		settled = true
		return Result{Fallback: true, Title: plainTitle(len(words)), Price: estimate}, nil
	}
	valid := validate(log, words, got)
	title := valid.Title
	if title == "" {
		title = plainTitle(len(words))
		valid.Title = title
	}
	if err := store(ctx, cfg.DB, cfg.OwnerID, cfg.EpisodeID, words, valid); err != nil {
		return Result{}, err
	}
	if err := cfg.Budgets.Settle(estimate, estimate); err != nil {
		return Result{}, fmt.Errorf("editorial: run: settle: %w", err)
	}
	settled = true
	return Result{Title: title, Cuts: len(valid.Cuts), Price: estimate}, nil
}

// loadWords reads the episode edit words ordered by start. The prompt
// numbers them by position, and the proposals point back the same way.
func loadWords(ctx context.Context, db *sql.DB, episodeID string) ([]word, error) {
	rows, err := db.QueryContext(ctx,
		"SELECT text, start_ms, end_ms FROM words WHERE episode_id = ? AND source = ? ORDER BY start_ms ASC, rowid ASC",
		episodeID, "edit")
	if err != nil {
		return nil, fmt.Errorf("editorial: load words: %w", err)
	}
	defer rows.Close()
	var out []word
	for rows.Next() {
		var w word
		if err := rows.Scan(&w.Text, &w.StartMs, &w.EndMs); err != nil {
			return nil, fmt.Errorf("editorial: load words: %w", err)
		}
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("editorial: load words: %w", err)
	}
	return out, nil
}
