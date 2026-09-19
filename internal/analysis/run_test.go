package analysis_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nrynss/keel/cost"
	"github.com/nrynss/reprise/internal/analysis"
	"github.com/nrynss/reprise/internal/assemblyai"
)

// fakeBudget records every hold and frees nothing silently.
type fakeBudget struct {
	mu       sync.Mutex
	reserved []cost.Price
	settled  [][2]cost.Price
	released []cost.Price
	refuse   error
}

func (b *fakeBudget) Reserve(p cost.Price) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.reserved = append(b.reserved, p)
	return b.refuse
}

func (b *fakeBudget) Settle(held, actual cost.Price) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.settled = append(b.settled, [2]cost.Price{held, actual})
	return nil
}

func (b *fakeBudget) Release(p cost.Price) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.released = append(b.released, p)
}

// probeRates prices chapter tokens at the measured gateway rates.
func probeRates() analysis.Rates {
	return analysis.Rates{PromptPerMillionUSD: 0.10, CompletionPerMillionUSD: 0.50}
}

// episodeResult builds one canned completion over 40 words across 44
// seconds with entities, key phrases, and a summary on the same call.
func episodeResult() analysis.TranscriptResult {
	words := make([]analysis.Word, 0, 40)
	for i := range 40 {
		start := int64(500 + i*1100)
		words = append(words, analysis.Word{Text: "word", StartMs: start, EndMs: start + 700, Confidence: 0.99})
	}
	words[5].Text = "Mara"
	words[20].Text = "Lisbon"
	return analysis.TranscriptResult{
		ID:    "tx-scripted",
		Text:  "forty words across an episode",
		Words: words,
		Entities: []analysis.Entity{
			{Type: "person_name", Text: "Mara", StartMs: 6000, EndMs: 6700},
			{Type: "location", Text: "Lisbon", StartMs: 22500, EndMs: 23200},
		},
		Phrases: []analysis.KeyPhrase{{
			Text: "tide charts", Rank: 0.5, Count: 2,
			Spans: []analysis.Span{{StartMs: 9300, EndMs: 10000}, {StartMs: 33500, EndMs: 34200}},
		}},
		Summary:           analysis.Summary{Headline: "A late crossing", Bullets: []string{"Mara studies charts."}, Block: "Mara studies."},
		AudioDurationSecs: 45,
		Raw:               []byte(`{"id":"tx-scripted","status":"completed"}`),
	}
}

// episodeDrafts returns four chapter drafts across the episode.
func episodeDrafts() []analysis.ChapterDraft {
	return []analysis.ChapterDraft{
		{Title: "Arrival", StartMs: 400},
		{Title: "Crossing", StartMs: 11500},
		{Title: "Storm", StartMs: 23000},
		{Title: "Return", StartMs: 33500},
	}
}

// deletedResult answers the post-delete fetch with the marker text.
func deletedResult() analysis.TranscriptResult {
	return analysis.TranscriptResult{ID: "tx-scripted", Text: assemblyai.DeletedText}
}

// runConfig wires one full pass over the fixture render bytes.
func runConfig(t *testing.T, db *sql.DB, batch *analysis.ScriptedTranscriber, chapterer *analysis.ScriptedChapterer, budgets *fakeBudget, saveRaw analysis.RawSink) analysis.Config {
	t.Helper()
	return analysis.Config{
		DB:           db,
		Batch:        batch,
		Chapters:     chapterer,
		Budgets:      budgets,
		GatewayModel: "qwen-test",
		Rates:        probeRates(),
		OwnerID:      "owner-a",
		EpisodeID:    "ep-1",
		Render: func(context.Context, string, string) (analysis.RenderedFile, error) {
			return analysis.RenderedFile{Audio: fixtureRender(t), DurationSecs: 45}, nil
		},
		PollInterval: time.Millisecond,
		SaveRaw:      saveRaw,
	}
}

// TestRunStoresFullAnalysis pins the whole pass: budget reserved before
// the calls, one creation call buying every feature, words stored under
// the rendered source, chapters shaped onto words, mentions with
// rendered offsets, both receipts kept, the provider copy deleted and
// confirmed, and the reservation settled.
func TestRunStoresFullAnalysis(t *testing.T) {
	t.Parallel()
	db := openDiary(t)
	addOwner(t, db, "owner-a")
	addEpisode(t, db, "ep-1", "owner-a", 1, "draft")
	batch := &analysis.ScriptedTranscriber{Completion: episodeResult(), Deleted: deletedResult()}
	chapterer := &analysis.ScriptedChapterer{
		Drafts: episodeDrafts(),
		Usage:  analysis.Usage{PromptTokens: 90, CompletionTokens: 222},
		Raw:    []byte(`{"choices":[{"message":{"content":"chapters"}}]}`),
	}
	budgets := &fakeBudget{}
	receipts := make(map[string][]byte)
	result, err := analysis.Run(t.Context(), runConfig(t, db, batch, chapterer, budgets,
		func(_ context.Context, name string, raw []byte) error {
			receipts[name] = append([]byte(nil), raw...)
			return nil
		}))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.TranscriptID != "tx-scripted" || result.Words != 40 || result.Chapters != 4 || result.Mentions != 4 {
		t.Fatalf("result = %+v, want tx-scripted with 40 words, 4 chapters, 4 mentions", result)
	}
	if batch.Uploads != 1 || batch.Creates != 1 || batch.Deletes != 1 || chapterer.Calls != 1 {
		t.Fatalf("provider calls upload=%d create=%d chapters=%d delete=%d, want one each",
			batch.Uploads, batch.Creates, chapterer.Calls, batch.Deletes)
	}
	if len(batch.Audio) == 0 {
		t.Fatal("upload carried no render bytes")
	}
	if batch.AudioURL == "" {
		t.Fatal("create carried no upload URL")
	}
	if chapterer.Model != "qwen-test" {
		t.Fatalf("chapter model = %q, want the settings model", chapterer.Model)
	}
	if !strings.Contains(chapterer.Req.Transcript, "Mara") {
		t.Fatal("chapter prompt misses the rendered transcript words")
	}
	if len(receipts["transcript"]) == 0 || len(receipts["chapters"]) == 0 {
		t.Fatalf("receipts = %v, want both provider responses kept", receipts)
	}
	if batch.DeleteIDs[0] != "tx-scripted" {
		t.Fatalf("deleted ids = %v, want tx-scripted", batch.DeleteIDs)
	}
	stored, err := analysis.LoadWords(t.Context(), db, "ep-1")
	if err != nil {
		t.Fatalf("load words: %v", err)
	}
	if len(stored) != 40 || stored[5].Text != "Mara" || stored[5].StartMs != 6000 {
		t.Fatalf("stored words miss Mara at 6000: %+v", stored[5])
	}
	rows, err := db.QueryContext(t.Context(),
		"SELECT kind, word_offset, quote FROM mentions WHERE episode_id = 'ep-1' ORDER BY word_offset ASC")
	if err != nil {
		t.Fatalf("read mentions: %v", err)
	}
	defer rows.Close()
	type row struct {
		kind   string
		offset int
		quote  string
	}
	var mentions []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.kind, &r.offset, &r.quote); err != nil {
			t.Fatalf("scan mention: %v", err)
		}
		mentions = append(mentions, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read mentions: %v", err)
	}
	want := []row{
		{"person_name", 5, "Mara"},
		{"keyphrase", 8, "tide charts"},
		{"location", 20, "Lisbon"},
		{"keyphrase", 30, "tide charts"},
	}
	if len(mentions) != len(want) {
		t.Fatalf("mentions = %+v, want %+v", mentions, want)
	}
	for i := range want {
		if mentions[i] != want[i] {
			t.Fatalf("mentions = %+v, want %+v", mentions, want)
		}
	}
	var chaptersJSON string
	if err := db.QueryRowContext(t.Context(),
		"SELECT chapters FROM analyses WHERE episode_id = 'ep-1'").Scan(&chaptersJSON); err != nil {
		t.Fatalf("read analyses: %v", err)
	}
	var chapters []struct {
		Title   string `json:"title"`
		StartMs int64  `json:"start_ms"`
	}
	if err := json.Unmarshal([]byte(chaptersJSON), &chapters); err != nil {
		t.Fatalf("decode chapters: %v", err)
	}
	if len(chapters) != 4 || chapters[0].StartMs != 0 || chapters[0].Title != "Arrival" {
		t.Fatalf("chapters = %+v, want four opening at zero", chapters)
	}
	for _, c := range chapters[1:] {
		found := false
		for _, w := range stored {
			if w.StartMs == c.StartMs {
				found = true
			}
		}
		if !found {
			t.Fatalf("chapter %+v starts off the stored words, so a seek would miss", c)
		}
	}
	budgets.mu.Lock()
	defer budgets.mu.Unlock()
	if len(budgets.reserved) != 1 || len(budgets.settled) != 1 || len(budgets.released) != 0 {
		t.Fatalf("budget holds %+v, want one reserve and one settle", budgets)
	}
	if budgets.settled[0][0] != budgets.reserved[0] {
		t.Fatal("settle does not free the held reservation")
	}
	if budgets.settled[0][1] <= 0 || budgets.settled[0][1] > budgets.reserved[0] {
		t.Fatalf("settled price = %v, want a positive spend within the reserve", budgets.settled[0][1])
	}
	if result.Price != budgets.settled[0][1] {
		t.Fatalf("result price = %v, want the settled price", result.Price)
	}
}

// TestRunRefusesWithoutBudget checks a refused reservation reaches no
// provider endpoint and stores no words.
func TestRunRefusesWithoutBudget(t *testing.T) {
	t.Parallel()
	db := openDiary(t)
	addOwner(t, db, "owner-a")
	addEpisode(t, db, "ep-1", "owner-a", 1, "draft")
	batch := &analysis.ScriptedTranscriber{Completion: episodeResult(), Deleted: deletedResult()}
	chapterer := &analysis.ScriptedChapterer{Drafts: episodeDrafts()}
	budgets := &fakeBudget{refuse: cost.ErrOverBudget}
	_, err := analysis.Run(t.Context(), runConfig(t, db, batch, chapterer, budgets,
		func(context.Context, string, []byte) error { return nil }))
	if !errors.Is(err, cost.ErrOverBudget) {
		t.Fatalf("run error = %v, want ErrOverBudget", err)
	}
	if batch.Uploads != 0 || batch.Creates != 0 || chapterer.Calls != 0 {
		t.Fatal("provider calls ran after a refused reservation")
	}
	stored, err := analysis.LoadWords(t.Context(), db, "ep-1")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(stored) != 0 {
		t.Fatalf("stored %d words, want none after refusal", len(stored))
	}
}

// TestRunKeepsProviderCopyOnReceiptFailure checks a receipt store
// failure skips the delete, so the provider copy survives for an
// explicit retry, and frees the reservation.
func TestRunKeepsProviderCopyOnReceiptFailure(t *testing.T) {
	t.Parallel()
	db := openDiary(t)
	addOwner(t, db, "owner-a")
	addEpisode(t, db, "ep-1", "owner-a", 1, "draft")
	batch := &analysis.ScriptedTranscriber{Completion: episodeResult(), Deleted: deletedResult()}
	chapterer := &analysis.ScriptedChapterer{Drafts: episodeDrafts()}
	budgets := &fakeBudget{}
	receiptErr := errors.New("disk full")
	_, err := analysis.Run(t.Context(), runConfig(t, db, batch, chapterer, budgets,
		func(context.Context, string, []byte) error { return receiptErr }))
	if !errors.Is(err, receiptErr) {
		t.Fatalf("run error = %v, want the receipt failure", err)
	}
	if !strings.Contains(err.Error(), "tx-scripted") {
		t.Fatalf("run error = %v, want it to name tx-scripted for the explicit retry", err)
	}
	if batch.Deletes != 0 {
		t.Fatal("run deleted the provider copy before persisting the receipt")
	}
	budgets.mu.Lock()
	defer budgets.mu.Unlock()
	if len(budgets.settled) != 0 || len(budgets.released) != 1 {
		t.Fatalf("budget holds %+v, want one release and no settle", budgets)
	}
}

// TestRunConfirmsDelete checks a provider copy that stays readable
// after the delete call fails the run instead of settling.
func TestRunConfirmsDelete(t *testing.T) {
	t.Parallel()
	db := openDiary(t)
	addOwner(t, db, "owner-a")
	addEpisode(t, db, "ep-1", "owner-a", 1, "draft")
	readable := episodeResult()
	batch := &analysis.ScriptedTranscriber{Completion: episodeResult(), Deleted: readable}
	chapterer := &analysis.ScriptedChapterer{Drafts: episodeDrafts(), Raw: []byte(`{}`)}
	budgets := &fakeBudget{}
	_, err := analysis.Run(t.Context(), runConfig(t, db, batch, chapterer, budgets,
		func(context.Context, string, []byte) error { return nil }))
	if !errors.Is(err, analysis.ErrDeleteUnconfirmed) {
		t.Fatalf("run error = %v, want ErrDeleteUnconfirmed", err)
	}
	budgets.mu.Lock()
	defer budgets.mu.Unlock()
	if len(budgets.settled) != 0 {
		t.Fatal("run settled spend while a provider copy lingers")
	}
}

// TestRunKeepsCopyOnChapterFailure checks a failed chapter call keeps
// the provider copy and frees the reservation.
func TestRunKeepsCopyOnChapterFailure(t *testing.T) {
	t.Parallel()
	db := openDiary(t)
	addOwner(t, db, "owner-a")
	addEpisode(t, db, "ep-1", "owner-a", 1, "draft")
	batch := &analysis.ScriptedTranscriber{Completion: episodeResult(), Deleted: deletedResult()}
	chapterer := &analysis.ScriptedChapterer{Err: errors.New("gateway down")}
	budgets := &fakeBudget{}
	_, err := analysis.Run(t.Context(), runConfig(t, db, batch, chapterer, budgets,
		func(context.Context, string, []byte) error { return nil }))
	if err == nil || !strings.Contains(err.Error(), "gateway down") {
		t.Fatalf("run error = %v, want the chapter failure", err)
	}
	if batch.Deletes != 0 {
		t.Fatal("run deleted the provider copy before chapters landed")
	}
	budgets.mu.Lock()
	defer budgets.mu.Unlock()
	if len(budgets.settled) != 0 || len(budgets.released) != 1 {
		t.Fatalf("budget holds %+v, want one release and no settle", budgets)
	}
}

// TestEstimatePricesBatchAndGateway checks one audio hour costs the
// batch rate and the chapter cap prices a million tokens at the dearer
// rate.
func TestEstimatePricesBatchAndGateway(t *testing.T) {
	t.Parallel()
	if got := analysis.Estimate(3600, 0, analysis.Rates{}); got != cost.USD(0.21) {
		t.Fatalf("hour estimate = %v, want the batch hourly rate", got)
	}
	if got := analysis.Estimate(3600, 1000000, analysis.Rates{PromptPerMillionUSD: 1, CompletionPerMillionUSD: 2}); got != cost.USD(0.21)+cost.USD(2) {
		t.Fatalf("capped estimate = %v, want batch plus the completion rate cap", got)
	}
	if got := analysis.Estimate(0, 0, analysis.Rates{}); got != cost.Price(0) {
		t.Fatalf("zero estimate = %v, want nothing", got)
	}
}

// TestKindNeverResumes pins the job kind: two at once, never
// idempotent, no resume path, one attempt. A restart marks the job
// interrupted and no path repays the call on its own.
func TestKindNeverResumes(t *testing.T) {
	t.Parallel()
	kind := analysis.Kind
	if kind.Limit != 2 {
		t.Fatalf("kind limit = %d, want 2", kind.Limit)
	}
	if kind.Idempotent {
		t.Fatal("kind is idempotent, which would repay a restart")
	}
	if kind.Resume != nil {
		t.Fatal("kind carries a resume path for a paid call")
	}
	if kind.MaxAttempts != 0 {
		t.Fatalf("kind max attempts = %d, want one attempt", kind.MaxAttempts)
	}
}

// TestUploaderMatchesBatchClient pins the upload seam against the real
// batch client, so the production binding takes the same shape.
func TestUploaderMatchesBatchClient(t *testing.T) {
	t.Parallel()
	client, err := assemblyai.NewBatchClient(assemblyai.Config{APIKey: "test-key"})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	var _ analysis.Uploader = client
}
