package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"

	"github.com/nrynss/keel/cost"
	"github.com/nrynss/keel/id"
	"github.com/nrynss/reprise/internal/analysis"
)

// CommitmentKind marks mentions holding things the speaker said they
// would do. The analysis pass never writes this kind, so marking owns
// every row under it.
const CommitmentKind = "commitment"

// DoingKind marks mentions holding a reported outcome. Each one exists
// as evidence behind a resolution row.
const DoingKind = "doing"

// KeyphraseKind marks mentions built from key phrase occurrences. It
// repeats the analysis kind, so both passes group one surface.
const KeyphraseKind = "keyphrase"

// CommitmentRequest carries one marking call. The transcript holds the
// episode rendered words in order, so the model reads what people hear.
type CommitmentRequest struct {
	// Transcript is the episode text the model marks.
	Transcript string
}

// CommitmentAnswer carries one marking answer. JSON holds the raw model
// text, kept for the receipt store.
type CommitmentAnswer struct {
	// JSON is the raw answer text.
	JSON string
}

// ResolutionRequest carries one resolution call. Commitment names the
// open thing, and Transcript holds the later episode text that may
// report doing it.
type ResolutionRequest struct {
	// Commitment quotes the open thing in the speaker's own words.
	Commitment string
	// Transcript is the later episode text the model judges.
	Transcript string
}

// ResolutionAnswer carries one resolution answer. JSON holds the raw
// model text, kept for the receipt store.
type ResolutionAnswer struct {
	// JSON is the raw answer text.
	JSON string
}

// Model marks candidates in a transcript. The provider package satisfies
// it in production. Tests bind a scripted answer, so marking runs
// offline with no key and no network.
type Model interface {
	// GenerateCommitments lists things the speaker said they would do,
	// with exact quotes.
	GenerateCommitments(ctx context.Context, model string, req CommitmentRequest) (CommitmentAnswer, error)
	// GenerateResolution judges whether the later transcript reports
	// doing the commitment, with the evidence quote.
	GenerateResolution(ctx context.Context, model string, req ResolutionRequest) (ResolutionAnswer, error)
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

// Rates prices marking tokens in US dollars per million tokens. The
// wiring fills them from the model catalog beside the model id, so no
// price lives in code.
type Rates struct {
	// PromptPerMillionUSD prices one million prompt tokens.
	PromptPerMillionUSD float64
	// CompletionPerMillionUSD prices one million completion tokens.
	CompletionPerMillionUSD float64
	// MaxOutputTokens caps the answer length the estimate books.
	MaxOutputTokens int
}

// Estimate prices one marking call from its input length and the answer
// cap. Four characters book one prompt token. The caller reserves this
// before the provider call.
func Estimate(inputChars int, rates Rates) cost.Price {
	if inputChars < 0 {
		inputChars = 0
	}
	promptTokens := float64(inputChars) / 4
	completionTokens := float64(rates.MaxOutputTokens)
	if completionTokens < 0 {
		completionTokens = 0
	}
	dollars := promptTokens*rates.PromptPerMillionUSD/1e6 +
		completionTokens*rates.CompletionPerMillionUSD/1e6
	if dollars <= 0 {
		return cost.Price(0)
	}
	return cost.Price(math.Ceil(dollars * 1e9))
}

// RawSink persists the provider response bytes on receipt. The wiring
// points it at the episode media area, because the answer is the record
// of what the money bought.
type RawSink func(ctx context.Context, raw []byte) error

// CommitmentCandidate is one model proposed thing to do. Quote must sit
// in the episode words before it counts.
type CommitmentCandidate struct {
	// Quote carries the speaker's own words, quoted exactly.
	Quote string
	// Text names the thing to do in one line.
	Text string
}

// VerifiedCommitment is one candidate whose quote sits in the episode
// words. Offset points at the first quoted word, so the thread links to
// the moment it was said.
type VerifiedCommitment struct {
	// Quote carries the speaker's own words, quoted exactly.
	Quote string
	// Text names the thing to do in one line.
	Text string
	// Offset is the rendered word index where the quote starts.
	Offset int
}

// commitmentPayload decodes the marking answer. Unknown fields fail the
// parse, so a drifted shape cannot slide past silently.
type commitmentPayload struct {
	// Commitments holds every proposed thing to do.
	Commitments []CommitmentCandidate `json:"commitments"`
}

// ParseCommitments decodes one marking answer. A blank answer or one that
// is not JSON fails with ErrModel. Candidates keep their order, and
// verification decides each one later.
func ParseCommitments(raw string) ([]CommitmentCandidate, error) {
	var payload commitmentPayload
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return nil, fmt.Errorf("memory: parse commitments: %w: %w", ErrModel, err)
	}
	out := make([]CommitmentCandidate, 0, len(payload.Commitments))
	for _, cand := range payload.Commitments {
		out = append(out, CommitmentCandidate{
			Quote: strings.TrimSpace(cand.Quote),
			Text:  strings.TrimSpace(cand.Text),
		})
	}
	return out, nil
}

// VerifyCandidates keeps candidates whose quote sits in words as one
// contiguous run, with the offset of its first word. Quote-less and
// drifting candidates come back as rejected and are never stored. The
// pin is the rejection, not the log line about it.
func VerifyCandidates(candidates []CommitmentCandidate, words []string) (accepted []VerifiedCommitment, rejected []CommitmentCandidate) {
	for _, cand := range candidates {
		offset := QuoteOffset(cand.Quote, words)
		if cand.Quote == "" || cand.Text == "" || offset < 0 {
			rejected = append(rejected, cand)
			continue
		}
		accepted = append(accepted, VerifiedCommitment{Quote: cand.Quote, Text: cand.Text, Offset: offset})
	}
	return accepted, rejected
}

// Transcript joins rendered words into the text the model marks. The
// words arrive in episode order, so quotes point back into them.
func Transcript(words []string) string {
	return strings.Join(words, " ")
}

// CommitmentPrompt tells the model how to mark. The prompt asks for
// exact quotes only, so verification can hold every candidate to the
// words.
func CommitmentPrompt(transcript string) string {
	return "Read the transcript of one personal podcast episode. List the concrete things the speaker " +
		"said they would do. Quote the speaker exactly, using only words from the transcript. " +
		"Answer with JSON only, matching this shape: " +
		`{"commitments": [{"quote": "the speaker's own words", "text": "the thing to do in one line"}]}. ` +
		"Return an empty list when the speaker promised nothing.\n\nTranscript:\n" + transcript
}

// ResolutionPrompt tells the model how to judge. The prompt asks for the
// evidence quote only when the transcript reports doing the thing.
func ResolutionPrompt(commitment, transcript string) string {
	return "The speaker once said: " + commitment + "\n\n" +
		"Read the transcript of a later episode. Decide whether the speaker reports doing that thing. " +
		"Quote the speaker exactly, using only words from the transcript. " +
		"Answer with JSON only, matching this shape: " +
		`{"done": true or false, "quote": "the evidence in the speaker's own words, or empty"}. ` +
		"\n\nTranscript:\n" + transcript
}

// resolutionPayload decodes the resolution answer. Unknown fields fail
// the parse, so a drifted shape cannot slide past silently.
type resolutionPayload struct {
	// Done reports whether the transcript reports doing the thing.
	Done bool `json:"done"`
	// Quote carries the evidence in the speaker's own words.
	Quote string `json:"quote"`
}

// ParseResolution decodes one resolution answer. A blank answer or one
// that is not JSON fails with ErrModel.
func ParseResolution(raw string) (resolutionPayload, error) {
	var payload resolutionPayload
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return resolutionPayload{}, fmt.Errorf("memory: parse resolution: %w: %w", ErrModel, err)
	}
	payload.Quote = strings.TrimSpace(payload.Quote)
	return payload, nil
}

// renderedTexts loads the episode rendered word texts in episode order.
// Marking and verification read the same order the analysis pass stored.
func renderedTexts(ctx context.Context, db *sql.DB, episodeID string) ([]string, error) {
	stored, err := analysis.LoadWords(ctx, db, episodeID)
	if err != nil {
		return nil, fmt.Errorf("memory: load words: %w", err)
	}
	out := make([]string, 0, len(stored))
	for _, word := range stored {
		out = append(out, word.Text)
	}
	return out, nil
}

// StoreCommitments swaps the episode commitment mentions for found in one
// transaction. A rerun writes the same rows again instead of doubling
// them, so a repeated call stays safe. Rows under other kinds stay
// untouched.
func StoreCommitments(ctx context.Context, db *sql.DB, ownerID, episodeID string, found []VerifiedCommitment) error {
	if db == nil || ownerID == "" || episodeID == "" {
		return fmt.Errorf("memory: store commitments: %w", ErrInvalid)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("memory: store commitments: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		"DELETE FROM mentions WHERE episode_id = ? AND kind = ?", episodeID, CommitmentKind); err != nil {
		return fmt.Errorf("memory: store commitments: %w", err)
	}
	for _, item := range found {
		mentionID, err := id.New()
		if err != nil {
			return fmt.Errorf("memory: store commitments: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO mentions (id, owner_id, episode_id, kind, word_offset, quote) VALUES (?, ?, ?, ?, ?, ?)",
			mentionID, ownerID, episodeID, CommitmentKind, item.Offset, item.Quote); err != nil {
			return fmt.Errorf("memory: store commitments: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("memory: store commitments: %w", err)
	}
	return nil
}

// MarkConfig carries one episode marking run.
type MarkConfig struct {
	// DB is the diary writer holding words and mentions.
	DB *sql.DB
	// Model marks candidates in the transcript.
	Model Model
	// Budgets refuses the call when the owner or global ceiling is full.
	Budgets Budget
	// Rates prices the call from the model catalog.
	Rates Rates
	// ModelID names the marking model from settings, never from code.
	ModelID string
	// OwnerID scopes the words read and the mentions written.
	OwnerID string
	// EpisodeID scopes the episode marked.
	EpisodeID string
	// SaveRaw persists the provider response on receipt.
	SaveRaw RawSink
	// Logger records rejected candidates. Nil means the default logger.
	Logger *slog.Logger
}

// MarkResult reports one finished marking run.
type MarkResult struct {
	// Accepted holds the verified commitments stored.
	Accepted []VerifiedCommitment
	// Rejected counts the candidates the words did not hold.
	Rejected int
	// Price is the settled model price for the call.
	Price cost.Price
}

// MarkEpisode marks one episode's commitments. It reserves budget first,
// and a refused reservation returns before any provider call. Candidates
// whose quote misses the words are rejected and logged, never stored.
// The receipt store runs before parsing, so the answer persists even
// when parsing fails. Marking makes no second attempt on any failure,
// so a restart never pays twice.
func MarkEpisode(ctx context.Context, cfg MarkConfig) (MarkResult, error) {
	if cfg.DB == nil || cfg.Model == nil || cfg.Budgets == nil {
		return MarkResult{}, fmt.Errorf("memory: mark episode: %w: missing store, model, or budget", ErrInvalid)
	}
	if cfg.ModelID == "" || cfg.OwnerID == "" || cfg.EpisodeID == "" {
		return MarkResult{}, fmt.Errorf("memory: mark episode: %w: empty model, owner, or episode", ErrInvalid)
	}
	if cfg.SaveRaw == nil {
		return MarkResult{}, fmt.Errorf("memory: mark episode: %w: missing receipt store", ErrInvalid)
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	words, err := renderedTexts(ctx, cfg.DB, cfg.EpisodeID)
	if err != nil {
		return MarkResult{}, err
	}
	if len(words) == 0 {
		return MarkResult{}, fmt.Errorf("memory: mark episode: %w", ErrNoWords)
	}
	transcript := Transcript(words)
	estimate := Estimate(len(transcript), cfg.Rates)
	if err := cfg.Budgets.Reserve(estimate); err != nil {
		return MarkResult{}, fmt.Errorf("memory: mark episode: %w", err)
	}
	settled := false
	defer func() {
		if !settled {
			cfg.Budgets.Release(estimate)
		}
	}()
	reply, err := cfg.Model.GenerateCommitments(ctx, cfg.ModelID, CommitmentRequest{Transcript: transcript})
	if err != nil {
		return MarkResult{}, fmt.Errorf("memory: mark episode: %w: %w", ErrModel, err)
	}
	if strings.TrimSpace(reply.JSON) == "" {
		return MarkResult{}, fmt.Errorf("memory: mark episode: %w: empty answer", ErrModel)
	}
	if err := cfg.SaveRaw(ctx, []byte(reply.JSON)); err != nil {
		if settleErr := cfg.Budgets.Settle(estimate, estimate); settleErr != nil {
			return MarkResult{}, fmt.Errorf("memory: mark episode: receipt store: %v: settle: %w", err, settleErr)
		}
		settled = true
		return MarkResult{}, fmt.Errorf("memory: mark episode: receipt store: %w", err)
	}
	candidates, err := ParseCommitments(reply.JSON)
	if err != nil {
		if settleErr := cfg.Budgets.Settle(estimate, estimate); settleErr != nil {
			return MarkResult{}, fmt.Errorf("memory: mark episode: settle: %w", settleErr)
		}
		settled = true
		return MarkResult{}, err
	}
	accepted, rejected := VerifyCandidates(candidates, words)
	for _, item := range rejected {
		log.Warn("memory: commitment quote misses the words, rejecting it", "quote", item.Quote)
	}
	if err := StoreCommitments(ctx, cfg.DB, cfg.OwnerID, cfg.EpisodeID, accepted); err != nil {
		if settleErr := cfg.Budgets.Settle(estimate, estimate); settleErr != nil {
			return MarkResult{}, fmt.Errorf("memory: mark episode: settle: %w", settleErr)
		}
		settled = true
		return MarkResult{}, err
	}
	if err := cfg.Budgets.Settle(estimate, estimate); err != nil {
		return MarkResult{}, fmt.Errorf("memory: mark episode: settle: %w", err)
	}
	settled = true
	return MarkResult{Accepted: accepted, Rejected: len(rejected), Price: estimate}, nil
}

// Commitment is one stored thing to do with its resolution, if any. The
// quote is the stored wording, and the offset points at its rendered
// words.
type Commitment struct {
	// ID is the commitment mention row id.
	ID string
	// EpisodeID is the episode holding the commitment.
	EpisodeID string
	// Number is the episode number, for speech and ordering.
	Number int
	// Offset is the rendered word index the mention points at.
	Offset int
	// Quote carries the speaker's own words, quoted exactly.
	Quote string
	// Done reports whether a later doing mention closed it.
	Done bool
	// Evidence carries the closing quote, or empty when still open.
	Evidence string
	// EvidenceNumber names the closing episode, or zero when still open.
	EvidenceNumber int
}

// loadCommitments reads every commitment mention for the owner with its
// episode number, oldest episode first.
func loadCommitments(ctx context.Context, db *sql.DB, ownerID string) ([]Commitment, error) {
	const query = `SELECT m.id, m.episode_id, e.number, m.word_offset, m.quote
		FROM mentions AS m
		JOIN episodes AS e ON e.id = m.episode_id
		WHERE m.owner_id = ? AND m.kind = ?
		ORDER BY e.number ASC, m.rowid ASC`
	rows, err := db.QueryContext(ctx, query, ownerID, CommitmentKind)
	if err != nil {
		return nil, fmt.Errorf("memory: load commitments: %w", err)
	}
	defer rows.Close()
	var out []Commitment
	for rows.Next() {
		var item Commitment
		if err := rows.Scan(&item.ID, &item.EpisodeID, &item.Number, &item.Offset, &item.Quote); err != nil {
			return nil, fmt.Errorf("memory: load commitments: %w", err)
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("memory: load commitments: %w", err)
	}
	return out, nil
}

// OpenCommitments returns the owner's commitments with no close on
// record, oldest episode first. The callback choice reads this list, so
// it prefers what the speaker still owes themselves.
func OpenCommitments(ctx context.Context, db *sql.DB, ownerID string) ([]Commitment, error) {
	if db == nil || ownerID == "" {
		return nil, fmt.Errorf("memory: open commitments: %w", ErrInvalid)
	}
	all, err := loadCommitments(ctx, db, ownerID)
	if err != nil {
		return nil, err
	}
	closed, err := resolutionKeys(ctx, db, ownerID)
	if err != nil {
		return nil, err
	}
	var out []Commitment
	for _, item := range all {
		if closed[item.ID] {
			continue
		}
		out = append(out, item)
	}
	return out, nil
}

// ResolvedCommitments returns the owner's commitments with a close on
// record, each carrying its evidence quote and episode. The gallery
// shows these beside the open ones, so the thread reads as kept or
// still owed.
func ResolvedCommitments(ctx context.Context, db *sql.DB, ownerID string) ([]Commitment, error) {
	if db == nil || ownerID == "" {
		return nil, fmt.Errorf("memory: resolved commitments: %w", ErrInvalid)
	}
	all, err := loadCommitments(ctx, db, ownerID)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]*Commitment, len(all))
	for i := range all {
		byID[all[i].ID] = &all[i]
	}
	const query = `SELECT r.commitment_mention_id, m.quote, e.number
		FROM resolutions AS r
		JOIN mentions AS m ON m.id = r.evidence_mention_id
		JOIN episodes AS e ON e.id = m.episode_id
		WHERE r.owner_id = ?`
	rows, err := db.QueryContext(ctx, query, ownerID)
	if err != nil {
		return nil, fmt.Errorf("memory: load resolutions: %w", err)
	}
	defer rows.Close()
	var out []Commitment
	for rows.Next() {
		var id, evidence string
		var number int
		if err := rows.Scan(&id, &evidence, &number); err != nil {
			return nil, fmt.Errorf("memory: load resolutions: %w", err)
		}
		item, found := byID[id]
		if !found {
			continue
		}
		got := *item
		got.Done = true
		got.Evidence = evidence
		got.EvidenceNumber = number
		out = append(out, got)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("memory: load resolutions: %w", err)
	}
	return out, nil
}

// ResolveConfig carries one resolution run.
type ResolveConfig struct {
	// DB is the diary writer holding words, mentions, and resolutions.
	DB *sql.DB
	// Model judges the later transcript.
	Model Model
	// Budgets refuses the call when the owner or global ceiling is full.
	Budgets Budget
	// Rates prices the call from the model catalog.
	Rates Rates
	// ModelID names the marking model from settings, never from code.
	ModelID string
	// OwnerID scopes every row the run reads and writes.
	OwnerID string
	// CommitmentID names the stored commitment mention to close.
	CommitmentID string
	// EpisodeID names the later episode that may report doing it.
	EpisodeID string
	// SaveRaw persists the provider response on receipt.
	SaveRaw RawSink
	// Logger records the outcome. Nil means the default logger.
	Logger *slog.Logger
}

// ResolveResult reports one finished resolution run.
type ResolveResult struct {
	// Done reports whether the later transcript closed the commitment.
	Done bool
	// Evidence carries the closing quote when done.
	Evidence string
	// Price is the settled model price for the call.
	Price cost.Price
}

// ResolveEpisode judges one later episode against one open commitment.
// It reserves budget first, and a refused reservation returns before any
// provider call. The evidence episode must be later than the commitment
// episode, so a backdated run fails with ErrInvalid before any provider
// call. A negative answer stores nothing and still settles the
// call. An evidence quote missing from the later words fails with
// ErrModel and stores nothing, because a close needs its proof in the
// words.
func ResolveEpisode(ctx context.Context, cfg ResolveConfig) (ResolveResult, error) {
	if cfg.DB == nil || cfg.Model == nil || cfg.Budgets == nil {
		return ResolveResult{}, fmt.Errorf("memory: resolve episode: %w: missing store, model, or budget", ErrInvalid)
	}
	if cfg.ModelID == "" || cfg.OwnerID == "" || cfg.CommitmentID == "" || cfg.EpisodeID == "" {
		return ResolveResult{}, fmt.Errorf("memory: resolve episode: %w: empty model, owner, commitment, or episode", ErrInvalid)
	}
	if cfg.SaveRaw == nil {
		return ResolveResult{}, fmt.Errorf("memory: resolve episode: %w: missing receipt store", ErrInvalid)
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	commitment, commitmentEpisodeID, err := loadCommitment(ctx, cfg.DB, cfg.OwnerID, cfg.CommitmentID)
	if err != nil {
		return ResolveResult{}, err
	}
	commitmentNumber, err := episodeNumber(ctx, cfg.DB, commitmentEpisodeID)
	if err != nil {
		return ResolveResult{}, err
	}
	evidenceNumber, err := episodeNumber(ctx, cfg.DB, cfg.EpisodeID)
	if err != nil {
		return ResolveResult{}, err
	}
	if evidenceNumber <= commitmentNumber {
		return ResolveResult{}, fmt.Errorf("memory: resolve episode: %w: evidence must land on a later episode", ErrInvalid)
	}
	words, err := renderedTexts(ctx, cfg.DB, cfg.EpisodeID)
	if err != nil {
		return ResolveResult{}, err
	}
	if len(words) == 0 {
		return ResolveResult{}, fmt.Errorf("memory: resolve episode: %w", ErrNoWords)
	}
	transcript := Transcript(words)
	estimate := Estimate(len(transcript)+len(commitment), cfg.Rates)
	if err := cfg.Budgets.Reserve(estimate); err != nil {
		return ResolveResult{}, fmt.Errorf("memory: resolve episode: %w", err)
	}
	settled := false
	defer func() {
		if !settled {
			cfg.Budgets.Release(estimate)
		}
	}()
	settle := func() error {
		if err := cfg.Budgets.Settle(estimate, estimate); err != nil {
			return fmt.Errorf("memory: resolve episode: settle: %w", err)
		}
		settled = true
		return nil
	}
	reply, err := cfg.Model.GenerateResolution(ctx, cfg.ModelID,
		ResolutionRequest{Commitment: commitment, Transcript: transcript})
	if err != nil {
		return ResolveResult{}, fmt.Errorf("memory: resolve episode: %w: %w", ErrModel, err)
	}
	if strings.TrimSpace(reply.JSON) == "" {
		return ResolveResult{}, fmt.Errorf("memory: resolve episode: %w: empty answer", ErrModel)
	}
	if err := cfg.SaveRaw(ctx, []byte(reply.JSON)); err != nil {
		if settleErr := settle(); settleErr != nil {
			return ResolveResult{}, fmt.Errorf("memory: resolve episode: receipt store: %v: %w", err, settleErr)
		}
		return ResolveResult{}, fmt.Errorf("memory: resolve episode: receipt store: %w", err)
	}
	proposal, err := ParseResolution(reply.JSON)
	if err != nil {
		if settleErr := settle(); settleErr != nil {
			return ResolveResult{}, settleErr
		}
		return ResolveResult{}, err
	}
	if !proposal.Done {
		log.Info("memory: later episode reports nothing done, leaving the commitment open")
		if err := settle(); err != nil {
			return ResolveResult{}, err
		}
		return ResolveResult{Price: estimate}, nil
	}
	offset := QuoteOffset(proposal.Quote, words)
	if proposal.Quote == "" || offset < 0 {
		if err := settle(); err != nil {
			return ResolveResult{}, err
		}
		return ResolveResult{}, fmt.Errorf("memory: resolve episode: %w: evidence misses the words", ErrModel)
	}
	if err := StoreResolution(ctx, cfg.DB, cfg.OwnerID, cfg.CommitmentID, cfg.EpisodeID, proposal.Quote, offset); err != nil {
		if settleErr := settle(); settleErr != nil {
			return ResolveResult{}, settleErr
		}
		return ResolveResult{}, err
	}
	if err := settle(); err != nil {
		return ResolveResult{}, err
	}
	return ResolveResult{Done: true, Evidence: proposal.Quote, Price: estimate}, nil
}

// loadCommitment reads one commitment quote with its episode for the
// owner. A missing row or a row under another kind fails with
// ErrNotFound.
func loadCommitment(ctx context.Context, db *sql.DB, ownerID, commitmentID string) (quote, episodeID string, err error) {
	const query = `SELECT quote, episode_id FROM mentions WHERE id = ? AND owner_id = ? AND kind = ?`
	if err := db.QueryRowContext(ctx, query, commitmentID, ownerID, CommitmentKind).Scan(&quote, &episodeID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", "", fmt.Errorf("memory: load commitment: %w", ErrNotFound)
		}
		return "", "", fmt.Errorf("memory: load commitment: %w", err)
	}
	return quote, episodeID, nil
}

// episodeNumber reads one episode number. An unknown episode fails with
// ErrInvalid, because resolution names stored episodes only.
func episodeNumber(ctx context.Context, db *sql.DB, episodeID string) (int, error) {
	var number int
	err := db.QueryRowContext(ctx, "SELECT number FROM episodes WHERE id = ?", episodeID).Scan(&number)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("memory: load episode number: %w: unknown episode", ErrInvalid)
	}
	if err != nil {
		return 0, fmt.Errorf("memory: load episode number: %w", err)
	}
	return number, nil
}
