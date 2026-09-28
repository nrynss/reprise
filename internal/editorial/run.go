package editorial

import (
	"context"
	"database/sql"
	"errors"
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
	// Rates prices the call tokens from the model catalog beside the
	// model id, the way chapters and marking receive theirs.
	Rates Rates
	// OwnerID scopes the words read and the proposals written.
	OwnerID string
	// EpisodeID scopes the proposals written.
	EpisodeID string
	// UserStem holds the speaker stem audio.
	UserStem []byte
	// HostStem holds the host stem audio.
	HostStem []byte
	// DurationSecs sizes the reservation at the audio token rate.
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
	// Price is the settled model price from the measured token usage.
	Price cost.Price
}

// Rates prices editorial tokens in US dollars per million tokens. The
// wiring fills them from the model catalog beside the model id, so no
// price lives in code.
type Rates struct {
	// PromptPerMillionUSD prices one million input tokens, audio included.
	PromptPerMillionUSD float64
	// CompletionPerMillionUSD prices one million output tokens, thinking
	// included.
	CompletionPerMillionUSD float64
}

// MaxOutputTokens caps the answer length the estimate books. It matches
// the answer cap the editorial call sends, so the reservation covers the
// dearest answer the model may return, thinking included.
const MaxOutputTokens = 8192

// Estimate prices one editorial call before any provider call. Audio
// bills about 62 tokens a second per stem, and the call carries two
// stems, so each audio second books 124 input tokens. The timeline books
// four characters a token. The answer may reach MaxOutputTokens, so the
// estimate books the full cap at the output rate. The caller reserves
// this before the provider call.
func Estimate(audioSeconds float64, timelineChars int, rates Rates) cost.Price {
	if audioSeconds < 0 {
		audioSeconds = 0
	}
	if timelineChars < 0 {
		timelineChars = 0
	}
	inputTokens := math.Ceil(audioSeconds*124) + float64(timelineChars)/4
	dollars := inputTokens*rates.PromptPerMillionUSD/1e6 +
		float64(MaxOutputTokens)*rates.CompletionPerMillionUSD/1e6
	if dollars <= 0 {
		return cost.Price(0)
	}
	return cost.USD(dollars)
}

// UsagePrice books one editorial answer from its measured token usage.
// The prompt tokens price at the input rate. Every other token the call
// billed prices at the output rate, thinking included.
func UsagePrice(usage gemini.Usage, rates Rates) cost.Price {
	completion := usage.Total - usage.Prompt
	if completion < 0 {
		completion = 0
	}
	dollars := float64(usage.Prompt)*rates.PromptPerMillionUSD/1e6 +
		float64(completion)*rates.CompletionPerMillionUSD/1e6
	if dollars <= 0 {
		return cost.Price(0)
	}
	return cost.USD(dollars)
}

// Run hears both stems, validates every proposal against the word
// timeline, and stores the draft. It reserves budget first, and a refused
// reservation returns before any provider call. Dangling proposals are
// dropped and logged, never stored. A model failure still leaves a
// renderable draft with no cuts and a plain title, and reports it through
// Result. A truncated answer was already billed, so that failure settles
// the estimate instead of releasing it. Run makes no second attempt on
// any failure, so a restart never pays twice.
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
	marked := timeline(words)
	estimate := Estimate(cfg.DurationSecs, len(marked), cfg.Rates)
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
		Timeline: marked,
	})
	if err != nil {
		log.Warn("editorial: model failed, leaving a renderable draft", "error", err)
		title := plainTitle(len(words))
		// A capped answer returns no text, but the call was billed.
		// Settle the same estimate a bad decode settles.
		billed := errors.Is(err, gemini.ErrTruncated)
		if storeErr := store(ctx, cfg.DB, cfg.OwnerID, cfg.EpisodeID, words, draft{Title: title}); storeErr != nil {
			if billed {
				if settleErr := cfg.Budgets.Settle(estimate, estimate); settleErr != nil {
					return Result{}, errors.Join(storeErr, fmt.Errorf("editorial: run: settle: %w", settleErr))
				}
				settled = true
			}
			return Result{}, storeErr
		}
		if !billed {
			return Result{Fallback: true, Title: title, Price: cost.Price(0)}, nil
		}
		if settleErr := cfg.Budgets.Settle(estimate, estimate); settleErr != nil {
			return Result{}, fmt.Errorf("editorial: run: settle: %w", settleErr)
		}
		settled = true
		return Result{Fallback: true, Title: title, Price: estimate}, nil
	}
	if err := cfg.SaveRaw(ctx, []byte(reply.JSON)); err != nil {
		// The call succeeded, so usage is known. Settle the measured
		// price rather than the estimate.
		actual := UsagePrice(reply.Usage, cfg.Rates)
		if settleErr := cfg.Budgets.Settle(estimate, actual); settleErr != nil {
			return Result{}, errors.Join(err, fmt.Errorf("editorial: run: settle: %w", settleErr))
		}
		settled = true
		return Result{}, fmt.Errorf("editorial: run: receipt store: %w", err)
	}
	got, err := decode(reply.JSON)
	if err != nil {
		log.Warn("editorial: answer unusable, leaving a renderable draft", "error", err)
		// The call succeeded, so usage is known. Settle the measured
		// price rather than the estimate.
		actual := UsagePrice(reply.Usage, cfg.Rates)
		if storeErr := store(ctx, cfg.DB, cfg.OwnerID, cfg.EpisodeID, words, draft{Title: plainTitle(len(words))}); storeErr != nil {
			if settleErr := cfg.Budgets.Settle(estimate, actual); settleErr != nil {
				return Result{}, errors.Join(storeErr, fmt.Errorf("editorial: run: settle: %w", settleErr))
			}
			settled = true
			return Result{}, storeErr
		}
		if settleErr := cfg.Budgets.Settle(estimate, actual); settleErr != nil {
			return Result{}, fmt.Errorf("editorial: run: settle: %w", settleErr)
		}
		settled = true
		return Result{Fallback: true, Title: plainTitle(len(words)), Price: actual}, nil
	}
	valid := validate(log, words, got)
	title := valid.Title
	if title == "" {
		title = plainTitle(len(words))
		valid.Title = title
	}
	if err := store(ctx, cfg.DB, cfg.OwnerID, cfg.EpisodeID, words, valid); err != nil {
		// The call succeeded, so usage is known. Settle the measured
		// price rather than the estimate.
		actual := UsagePrice(reply.Usage, cfg.Rates)
		if settleErr := cfg.Budgets.Settle(estimate, actual); settleErr != nil {
			return Result{}, errors.Join(err, fmt.Errorf("editorial: run: settle: %w", settleErr))
		}
		settled = true
		return Result{}, err
	}
	actual := UsagePrice(reply.Usage, cfg.Rates)
	if err := cfg.Budgets.Settle(estimate, actual); err != nil {
		return Result{}, fmt.Errorf("editorial: run: settle: %w", err)
	}
	settled = true
	return Result{Title: title, Cuts: len(valid.Cuts), Price: actual}, nil
}

// loadWords reads the episode edit words ordered by start. The prompt
// numbers them by position, and the proposals point back the same way.
func loadWords(ctx context.Context, db *sql.DB, episodeID string) ([]word, error) {
	rows, err := db.QueryContext(ctx,
		"SELECT text, start_ms, end_ms, speaker FROM words WHERE episode_id = ? AND source = ? ORDER BY start_ms ASC, rowid ASC",
		episodeID, "edit")
	if err != nil {
		return nil, fmt.Errorf("editorial: load words: %w", err)
	}
	defer rows.Close()
	var out []word
	for rows.Next() {
		var w word
		if err := rows.Scan(&w.Text, &w.StartMs, &w.EndMs, &w.Speaker); err != nil {
			return nil, fmt.Errorf("editorial: load words: %w", err)
		}
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("editorial: load words: %w", err)
	}
	return out, nil
}
