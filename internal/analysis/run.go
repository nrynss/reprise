package analysis

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

// Kind names the job kind the wiring registers for this pass. Two
// analyses run at once. The kind is not idempotent, so a restart marks
// the job interrupted and the episode waits for an explicit retry. No
// resume path exists, because a resumed call would pay for the same
// render twice.
var Kind = job.Kind{Limit: 2}

// ChapterMaxTokens caps the chapter answer length. The measured chapter
// call spent a few hundred tokens, so this ceiling leaves wide margin
// without inviting a runaway answer.
const ChapterMaxTokens = 800

// Budget bounds the paid calls. A cost Budget satisfies it directly.
type Budget interface {
	// Reserve holds the estimate before the call and refuses when the
	// estimate would pass the limit.
	Reserve(cost.Price) error
	// Settle books the measured price and frees the reservation.
	Settle(reserved, actual cost.Price) error
	// Release frees a reservation the caller never spent.
	Release(cost.Price)
}

// RawSink persists one provider response body on receipt. The wiring
// points it at the episode media area, because provider URLs expire.
// The name tells transcript and chapter receipts apart.
type RawSink func(ctx context.Context, name string, raw []byte) error

// RenderedFile is one render ready to analyse.
type RenderedFile struct {
	// Audio holds the render bytes to upload.
	Audio []byte
	// DurationSecs is the render length in seconds for pricing.
	DurationSecs float64
}

// RenderLocator resolves the episode render bytes. The wiring serves it
// from the renders row and the media store, and tests bind fixture
// bytes behind the same seam.
type RenderLocator func(ctx context.Context, ownerID, episodeID string) (RenderedFile, error)

// Config carries one analysis run.
type Config struct {
	// DB is the diary writer holding words, analyses, and mentions.
	DB *sql.DB
	// Batch talks to the provider batch API.
	Batch Transcriber
	// Chapters asks the chapter model over the rendered transcript.
	Chapters Chapterer
	// Budgets refuses the calls when the owner or global ceiling is full.
	Budgets Budget
	// GatewayModel names the chapter model from settings, never from code.
	GatewayModel string
	// Rates prices the chapter tokens for the settle step.
	Rates Rates
	// OwnerID scopes the words, analyses, and mentions written.
	OwnerID string
	// EpisodeID scopes the rows written.
	EpisodeID string
	// Render resolves the episode render bytes.
	Render RenderLocator
	// PollInterval sets the transcript poll gap. Zero polls every five seconds.
	PollInterval time.Duration
	// SaveRaw persists each provider response on receipt.
	SaveRaw RawSink
}

// Result reports one finished analysis.
type Result struct {
	// TranscriptID is the provider transcript id, deleted after storing.
	TranscriptID string
	// Words counts the rendered timeline words stored.
	Words int
	// Chapters counts the shaped episode chapters stored.
	Chapters int
	// Mentions counts the callback anchors stored.
	Mentions int
	// Price is the settled batch plus chapter price.
	Price cost.Price
}

// Estimate prices one analysis before any provider call. The batch part
// follows the batch hourly rate. The chapter part caps the answer at
// the completion rate, which bounds the dearer side.
func Estimate(audioSecs float64, maxTokens int, rates Rates) cost.Price {
	var batch cost.Price
	if audioSecs > 0 {
		perSecond := float64(cost.USD(assemblyai.BatchDollarsPerHour)) / 3600
		batch = cost.Price(math.Ceil(perSecond * audioSecs))
	}
	var chapters cost.Price
	if maxTokens > 0 {
		chapters = cost.USD(float64(maxTokens) / 1e6 * rates.CompletionPerMillionUSD)
	}
	return batch + chapters
}

// GatewayPrice books one chapter answer from its token usage.
func GatewayPrice(usage Usage, rates Rates) cost.Price {
	return cost.USD(float64(usage.PromptTokens)/1e6*rates.PromptPerMillionUSD +
		float64(usage.CompletionTokens)/1e6*rates.CompletionPerMillionUSD)
}

// Run analyses one episode render. It reserves budget first, and a
// refused reservation returns before any provider call. It transcribes
// the render with every feature on one creation call, persists the
// response on receipt, stores the rendered words, asks the chapter
// model over the rendered transcript, stores chapters plus mentions,
// then deletes the provider copy and confirms it reads deleted before
// settling. Any failure before the delete keeps the provider copy for
// an explicit retry and frees the reservation. Run makes no second
// attempt on any failure, so a restart never pays twice.
func Run(ctx context.Context, cfg Config) (Result, error) {
	if cfg.DB == nil || cfg.Batch == nil || cfg.Chapters == nil || cfg.Budgets == nil {
		return Result{}, fmt.Errorf("analysis: run: %w: missing store, clients, or budget", ErrInvalid)
	}
	if cfg.OwnerID == "" || cfg.EpisodeID == "" || cfg.GatewayModel == "" {
		return Result{}, fmt.Errorf("analysis: run: %w: empty owner, episode, or model", ErrInvalid)
	}
	if cfg.Render == nil || cfg.SaveRaw == nil {
		return Result{}, fmt.Errorf("analysis: run: %w: missing render locator or receipt store", ErrInvalid)
	}
	render, err := cfg.Render(ctx, cfg.OwnerID, cfg.EpisodeID)
	if err != nil {
		return Result{}, fmt.Errorf("analysis: run: %w", err)
	}
	if len(render.Audio) == 0 || render.DurationSecs <= 0 {
		return Result{}, fmt.Errorf("analysis: run: %w", ErrNoRender)
	}
	estimate := Estimate(render.DurationSecs, ChapterMaxTokens, cfg.Rates)
	if err := cfg.Budgets.Reserve(estimate); err != nil {
		return Result{}, fmt.Errorf("analysis: run: %w", err)
	}
	settled := false
	defer func() {
		if !settled {
			cfg.Budgets.Release(estimate)
		}
	}()
	uploadURL, err := cfg.Batch.Upload(ctx, bytes.NewReader(render.Audio))
	if err != nil {
		return Result{}, fmt.Errorf("analysis: run: %w", err)
	}
	transcriptID, err := cfg.Batch.Create(ctx, CreateRequest{AudioURL: uploadURL})
	if err != nil {
		return Result{}, fmt.Errorf("analysis: run: %w", err)
	}
	done, err := cfg.Batch.Wait(ctx, transcriptID, cfg.PollInterval)
	if err != nil {
		return Result{}, fmt.Errorf("analysis: run: %w", err)
	}
	if err := cfg.SaveRaw(ctx, "transcript", done.Raw); err != nil {
		return Result{}, fmt.Errorf("analysis: run %s: receipt store: %w", transcriptID, err)
	}
	for _, w := range done.Words {
		if w.EndMs < w.StartMs {
			return Result{}, fmt.Errorf("analysis: run %s word %q: %w", transcriptID, w.Text, ErrOrder)
		}
	}
	if err := ReplaceWords(ctx, cfg.DB, cfg.OwnerID, cfg.EpisodeID, done.Words); err != nil {
		return Result{}, err
	}
	segments := SegmentWords(done.Words)
	answer, err := cfg.Chapters.CompleteChapters(ctx, cfg.GatewayModel, ChapterRequest{
		Transcript:   PromptText(segments, render.DurationSecs),
		DurationSecs: render.DurationSecs,
		MaxTokens:    ChapterMaxTokens,
	})
	if err != nil {
		return Result{}, fmt.Errorf("analysis: run: chapters: %w", err)
	}
	if err := cfg.SaveRaw(ctx, "chapters", answer.Raw); err != nil {
		return Result{}, fmt.Errorf("analysis: run %s: receipt store: %w", transcriptID, err)
	}
	durationMs := int64(render.DurationSecs * 1000)
	chapters, err := NormalizeChapters(draftList(answer), done.Words, durationMs)
	if err != nil {
		return Result{}, err
	}
	mentions := MentionsFromEntities(done.Entities, done.Words)
	mentions = append(mentions, MentionsFromPhrases(done.Phrases, done.Words)...)
	saved := SavedAnalysis{
		TranscriptID: transcriptID,
		Chapters:     chapters,
		Summary:      done.Summary,
		Entities:     done.Entities,
		Phrases:      done.Phrases,
		Mentions:     mentions,
	}
	if err := StoreAnalysis(ctx, cfg.DB, cfg.OwnerID, cfg.EpisodeID, saved); err != nil {
		return Result{}, err
	}
	if err := cfg.Batch.Delete(ctx, transcriptID); err != nil {
		return Result{}, fmt.Errorf("analysis: run: %w", err)
	}
	after, err := cfg.Batch.Get(ctx, transcriptID)
	if err != nil {
		return Result{}, fmt.Errorf("analysis: run: confirm delete: %w", err)
	}
	if !deleted(after) {
		return Result{}, fmt.Errorf("analysis: run %s: %w", transcriptID, ErrDeleteUnconfirmed)
	}
	actual := batchPrice(billedSeconds(done.AudioDurationSecs, render.DurationSecs)) + GatewayPrice(answer.Usage, cfg.Rates)
	if err := cfg.Budgets.Settle(estimate, actual); err != nil {
		return Result{}, fmt.Errorf("analysis: run: settle: %w", err)
	}
	settled = true
	return Result{TranscriptID: transcriptID, Words: len(done.Words), Chapters: len(chapters), Mentions: len(mentions), Price: actual}, nil
}

// draftList reads the raw drafts from one chapter answer.
func draftList(answer ChapterAnswer) []ChapterDraft {
	return append([]ChapterDraft(nil), answer.Drafts...)
}

// deleted reports whether the transcript reads as provider-deleted.
// Deletion scrubs the content but keeps the row, so success looks like
// the marker text with no words rather than a missing row.
func deleted(tx TranscriptResult) bool {
	return tx.Text == assemblyai.DeletedText && len(tx.Words) == 0
}

// batchPrice prices heard audio seconds at the batch hourly rate,
// rounded up to the nanodollar.
func batchPrice(audioSecs float64) cost.Price {
	if audioSecs <= 0 {
		return cost.Price(0)
	}
	perSecond := float64(cost.USD(assemblyai.BatchDollarsPerHour)) / 3600
	return cost.Price(math.Ceil(perSecond * audioSecs))
}

// billedSeconds prefers the provider audio length when present, because
// the provider bills what it heard. It falls back to the render length.
func billedSeconds(providerSecs int64, renderSecs float64) float64 {
	if providerSecs > 0 {
		return float64(providerSecs)
	}
	return renderSecs
}
