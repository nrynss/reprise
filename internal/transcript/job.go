package transcript

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"math"
	"time"

	"github.com/nrynss/keel/cost"
	"github.com/nrynss/keel/job"
	"github.com/nrynss/reprise/internal/assemblyai"
)

// Kind names the job kind the wiring registers for this pass. Two edit
// passes run at once. The kind is not idempotent, so a restart marks the
// job interrupted and the episode waits for an explicit retry. No resume
// path exists, because a resumed call would pay for the same audio twice.
var Kind = job.Kind{Limit: 2}

// Budget bounds one paid call. A cost Budget satisfies it directly.
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
// points it at the episode media area, because provider URLs expire.
type RawSink func(ctx context.Context, raw []byte) error

// Config carries one edit transcript run.
type Config struct {
	// DB is the diary writer holding mentions, turns, and words.
	DB *sql.DB
	// Batch talks to the provider batch API.
	Batch *assemblyai.Client
	// Budgets refuses the call when the owner or global ceiling is full.
	Budgets Budget
	// OwnerID scopes the mentions read and the words written.
	OwnerID string
	// EpisodeID scopes the words written.
	EpisodeID string
	// Audio holds the user stem bytes to upload.
	Audio []byte
	// DurationSecs prices the call at the batch hourly rate.
	DurationSecs float64
	// Host carries the live reply timings for the host side.
	Host []HostReply
	// Offsets shifts each stem to the episode clock.
	Offsets Offsets
	// PollInterval sets the transcript poll gap. Zero polls every five seconds.
	PollInterval time.Duration
	// SaveRaw persists the provider response on receipt.
	SaveRaw RawSink
}

// Result reports one finished edit pass.
type Result struct {
	// TranscriptID is the provider transcript id, deleted after storing.
	TranscriptID string
	// Words counts the merged timeline words stored for the editor.
	Words int
	// Price is the settled batch price for the audio seconds.
	Price cost.Price
}

// Estimate prices audio seconds at the batch hourly rate, rounded up to
// the nanodollar. The caller reserves this before the provider call.
func Estimate(audioSeconds float64) cost.Price {
	if audioSeconds <= 0 {
		return cost.Price(0)
	}
	perSecond := float64(cost.USD(assemblyai.BatchDollarsPerHour)) / 3600
	return cost.Price(math.Ceil(perSecond * audioSeconds))
}

// Run transcribes the user stem, merges the host replies, stores one word
// timeline, and deletes the provider copy. It reserves budget first, and a
// refused reservation returns before any provider call. It persists the
// provider response on receipt, then confirms the delete with a fresh fetch
// before settling. Run makes no second attempt on any failure, so a restart
// never pays twice.
func Run(ctx context.Context, cfg Config) (Result, error) {
	if cfg.DB == nil || cfg.Batch == nil || cfg.Budgets == nil {
		return Result{}, fmt.Errorf("transcript: run: %w: missing store, client, or budget", ErrInvalid)
	}
	if cfg.OwnerID == "" || cfg.EpisodeID == "" {
		return Result{}, fmt.Errorf("transcript: run: %w: empty owner or episode", ErrInvalid)
	}
	if len(cfg.Audio) == 0 || cfg.DurationSecs <= 0 {
		return Result{}, fmt.Errorf("transcript: run: %w: empty audio or duration", ErrInvalid)
	}
	if cfg.SaveRaw == nil {
		return Result{}, fmt.Errorf("transcript: run: %w: missing receipt store", ErrInvalid)
	}
	keyterms, err := Keyterms(ctx, cfg.DB, cfg.OwnerID)
	if err != nil {
		return Result{}, err
	}
	estimate := Estimate(cfg.DurationSecs)
	if err := cfg.Budgets.Reserve(estimate); err != nil {
		return Result{}, fmt.Errorf("transcript: run: %w", err)
	}
	settled := false
	defer func() {
		if !settled {
			cfg.Budgets.Release(estimate)
		}
	}()
	uploadURL, err := cfg.Batch.Upload(ctx, bytes.NewReader(cfg.Audio))
	if err != nil {
		return Result{}, fmt.Errorf("transcript: run: %w", err)
	}
	transcriptID, err := cfg.Batch.Create(ctx, assemblyai.CreateRequest{AudioURL: uploadURL, Keyterms: keyterms})
	if err != nil {
		return Result{}, fmt.Errorf("transcript: run: %w", err)
	}
	tx, err := cfg.Batch.Wait(ctx, transcriptID, cfg.PollInterval)
	if err != nil {
		return Result{}, fmt.Errorf("transcript: run: %w", err)
	}
	if err := cfg.SaveRaw(ctx, tx.Raw); err != nil {
		return Result{}, fmt.Errorf("transcript: run %s: receipt store: %w", transcriptID, err)
	}
	user := make([]assemblyai.Word, 0, len(tx.Words))
	user = append(user, tx.Words...)
	merged, err := Merge(user, cfg.Host, cfg.Offsets)
	if err != nil {
		return Result{}, err
	}
	if err := Replace(ctx, cfg.DB, cfg.OwnerID, cfg.EpisodeID, merged); err != nil {
		return Result{}, err
	}
	if err := cfg.Batch.Delete(ctx, transcriptID); err != nil {
		return Result{}, fmt.Errorf("transcript: run: %w", err)
	}
	after, err := cfg.Batch.Get(ctx, transcriptID)
	if err != nil {
		return Result{}, fmt.Errorf("transcript: run: confirm delete: %w", err)
	}
	if !after.Deleted() {
		return Result{}, fmt.Errorf("transcript: run %s: %w", transcriptID, ErrDeleteUnconfirmed)
	}
	actual := Estimate(billedSeconds(tx.AudioDurationSecs, cfg.DurationSecs))
	if err := cfg.Budgets.Settle(estimate, actual); err != nil {
		return Result{}, fmt.Errorf("transcript: run: settle: %w", err)
	}
	settled = true
	return Result{TranscriptID: transcriptID, Words: len(merged), Price: actual}, nil
}

// billedSeconds prefers the provider audio length when present, because the
// provider bills what it heard. It falls back to the caller duration.
func billedSeconds(providerSecs int64, callerSecs float64) float64 {
	if providerSecs > 0 {
		return float64(providerSecs)
	}
	return callerSecs
}
