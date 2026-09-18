package editorial_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/nrynss/keel/cost"
	"github.com/nrynss/reprise/internal/editorial"
)

// fullAnswer proposes a cold open near twelve seconds, one cut, a title,
// notes, and a callback, all pointing inside a forty word timeline.
func fullAnswer() string {
	return `{"cold_open":{"start_word":0,"end_word":39,"reason":"a laugh opens it"},` +
		`"cuts":[{"start_word":5,"end_word":7,"reason":"false start"}],` +
		`"title":"Harbor Light","show_notes":"We talked about the ferry.",` +
		`"callback":{"start_word":10,"end_word":12,"quote":"w10 w11 w12","text":"the debt"}}`
}

// seedForty writes one owner, one episode, and forty edit words spaced
// 300 ms apart, so the full span measures just under twelve seconds.
func seedForty(t *testing.T, db *sql.DB) {
	t.Helper()
	addOwner(t, db, "owner-a")
	addEpisode(t, db, "ep-1", "owner-a", 3)
	addWords(t, db, "owner-a", "ep-1", 40, 300, 200)
}

// runCfg builds the run inputs the tests share.
func runCfg(db *sql.DB, model *scriptedModel, budgets *fakeBudget, receipt *[]byte) editorial.Config {
	return editorial.Config{
		DB:           db,
		Model:        model,
		Budgets:      budgets,
		ModelID:      "flash",
		OwnerID:      "owner-a",
		EpisodeID:    "ep-1",
		UserStem:     []byte{0x4f, 0x70, 0x75, 0x73},
		HostStem:     []byte{0x53, 0x74, 0x65, 0x6d},
		DurationSecs: 12,
		SaveRaw: func(_ context.Context, raw []byte) error {
			*receipt = append([]byte(nil), raw...)
			return nil
		},
	}
}

// episodeTitle reads the stored episode title.
func episodeTitle(t *testing.T, db *sql.DB) string {
	t.Helper()
	var title string
	if err := db.QueryRowContext(t.Context(), "SELECT title FROM episodes WHERE id = 'ep-1'").Scan(&title); err != nil {
		t.Fatalf("read title: %v", err)
	}
	return title
}

// TestRunStoresDraft pins the whole pass: the model hears both stems with
// the numbered timeline, every proposal lands on real words, cuts default
// to accepted, the callback persists as stored rows, the receipt is kept,
// and the reservation settles.
func TestRunStoresDraft(t *testing.T) {
	t.Parallel()
	db := openDiary(t)
	seedForty(t, db)
	model := &scriptedModel{answer: fullAnswer()}
	budgets := &fakeBudget{}
	var receipt []byte
	result, err := editorial.Run(t.Context(), runCfg(db, model, budgets, &receipt))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.Fallback || result.Title != "Harbor Light" || result.Cuts != 1 {
		t.Fatalf("result = %+v, want the stored draft with one cut", result)
	}
	model.mu.Lock()
	calls, timeline, userLen, hostLen, modelID := model.calls, model.timeline, model.userLen, model.hostLen, model.model
	model.mu.Unlock()
	if calls != 1 || modelID != "flash" || userLen == 0 || hostLen == 0 {
		t.Fatalf("model heard calls=%d model=%q user=%d host=%d, want one call with both stems", calls, modelID, userLen, hostLen)
	}
	if !strings.Contains(timeline, "[0]") || !strings.Contains(timeline, "[39]") {
		t.Fatalf("timeline misses the numbered words: %.200q", timeline)
	}
	if len(receipt) == 0 || !strings.Contains(string(receipt), "Harbor Light") {
		t.Fatal("receipt is empty or misses the answer")
	}
	if got := rowCount(t, db, "proposals", "ep-1"); got != 5 {
		t.Fatalf("proposals = %d, want cold open, cut, title, notes, and callback", got)
	}
	var start, end, words int
	if err := db.QueryRowContext(t.Context(),
		"SELECT start_word, end_word FROM proposals WHERE episode_id = 'ep-1' AND kind = 'cold_open'").Scan(&start, &end); err != nil {
		t.Fatalf("read cold open: %v", err)
	}
	if err := db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM words WHERE episode_id = 'ep-1'").Scan(&words); err != nil {
		t.Fatalf("count words: %v", err)
	}
	if start != 0 || end != 39 || end >= words {
		t.Fatalf("cold open = [%d, %d] over %d words, want [0, 39] on real words", start, end, words)
	}
	var decision string
	if err := db.QueryRowContext(t.Context(),
		`SELECT d.decision FROM decisions d JOIN proposals p ON p.id = d.proposal_id
		 WHERE p.episode_id = 'ep-1' AND p.kind = 'cut'`).Scan(&decision); err != nil {
		t.Fatalf("read decision: %v", err)
	}
	if decision != editorial.DecisionAccepted {
		t.Fatalf("decision = %q, want cuts accepted by default", decision)
	}
	var quote string
	var offset, used int
	if err := db.QueryRowContext(t.Context(),
		"SELECT quote, word_offset FROM mentions WHERE episode_id = 'ep-1' AND kind = 'callback'").Scan(&quote, &offset); err != nil {
		t.Fatalf("read mention: %v", err)
	}
	if quote != "w10 w11 w12" || offset != 10 {
		t.Fatalf("mention = %q at %d, want the quote with its word offset", quote, offset)
	}
	if err := db.QueryRowContext(t.Context(),
		`SELECT c.used FROM callbacks c JOIN mentions m ON m.id = c.mention_id
		 WHERE c.episode_id = 'ep-1'`).Scan(&used); err != nil {
		t.Fatalf("read callback: %v", err)
	}
	if used != 0 {
		t.Fatalf("callback used = %d, want unused until the next opening", used)
	}
	if title := episodeTitle(t, db); title != "Harbor Light" {
		t.Fatalf("title = %q, want the proposed title", title)
	}
	budgets.mu.Lock()
	defer budgets.mu.Unlock()
	if len(budgets.reserved) != 1 || len(budgets.settled) != 1 || len(budgets.released) != 0 {
		t.Fatalf("budget holds %+v, want one reserve and one settle", budgets)
	}
	want := editorial.Estimate(12)
	if budgets.reserved[0] != want {
		t.Fatalf("reserved = %v, want %v", budgets.reserved[0], want)
	}
	if budgets.settled[0] != [2]cost.Price{want, want} {
		t.Fatalf("settled = %v, want {%v %v}", budgets.settled[0], want, want)
	}
}

// TestRunDropsDanglingSpans checks a proposal pointing outside the word
// timeline is dropped and logged while the surviving cut still stores.
func TestRunDropsDanglingSpans(t *testing.T) {
	t.Parallel()
	db := openDiary(t)
	seedForty(t, db)
	model := &scriptedModel{answer: `{"cold_open":{"start_word":0,"end_word":400,"reason":"too long"},` +
		`"cuts":[{"start_word":5,"end_word":7,"reason":"false start"},{"start_word":38,"end_word":45,"reason":"ramble"}],` +
		`"title":"Harbor Light","show_notes":"Notes.","callback":{"start_word":90,"end_word":91,"quote":"ghost","text":"nothing"}}`}
	budgets := &fakeBudget{}
	var receipt []byte
	log, buf := captureLog()
	cfg := runCfg(db, model, budgets, &receipt)
	cfg.Logger = log
	result, err := editorial.Run(t.Context(), cfg)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.Fallback || result.Cuts != 1 {
		t.Fatalf("result = %+v, want one surviving cut and no fallback", result)
	}
	if logged := buf.String(); !strings.Contains(logged, "outside the timeline") {
		t.Fatalf("log misses the dropped spans: %q", logged)
	}
	if got := rowCount(t, db, "proposals", "ep-1"); got != 3 {
		t.Fatalf("proposals = %d, want the cut, the title, and the notes", got)
	}
	if got := rowCount(t, db, "mentions", "ep-1"); got != 0 {
		t.Fatalf("mentions = %d, want no callback without real words", got)
	}
}

// TestRunFallsBackOnModelFailure checks a failed call still leaves a
// renderable draft with no cuts and a plain title, and frees the
// reservation without settling.
func TestRunFallsBackOnModelFailure(t *testing.T) {
	t.Parallel()
	db := openDiary(t)
	seedForty(t, db)
	boom := errors.New("vertex unreachable")
	model := &scriptedModel{err: boom}
	budgets := &fakeBudget{}
	var receipt []byte
	result, err := editorial.Run(t.Context(), runCfg(db, model, budgets, &receipt))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !result.Fallback || result.Cuts != 0 {
		t.Fatalf("result = %+v, want a fallback draft with no cuts", result)
	}
	if title := episodeTitle(t, db); title != result.Title || !strings.Contains(title, "Untitled") {
		t.Fatalf("title = %q, want a plain title", title)
	}
	if got := rowCount(t, db, "proposals", "ep-1"); got != 1 {
		t.Fatalf("proposals = %d, want the title row only", got)
	}
	if got := rowCount(t, db, "decisions", "ep-1"); got != 0 {
		t.Fatalf("decisions = %d, want none without cuts", got)
	}
	budgets.mu.Lock()
	defer budgets.mu.Unlock()
	if len(budgets.reserved) != 1 || len(budgets.settled) != 0 || len(budgets.released) != 1 {
		t.Fatalf("budget holds %+v, want one reserve freed with no settle", budgets)
	}
}

// TestRunFallsBackOnUnusableAnswer checks JSON the pass cannot use still
// leaves a renderable draft. The call spent, so the reservation settles.
func TestRunFallsBackOnUnusableAnswer(t *testing.T) {
	t.Parallel()
	db := openDiary(t)
	seedForty(t, db)
	model := &scriptedModel{answer: "not json"}
	budgets := &fakeBudget{}
	var receipt []byte
	result, err := editorial.Run(t.Context(), runCfg(db, model, budgets, &receipt))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !result.Fallback {
		t.Fatalf("result = %+v, want a fallback draft", result)
	}
	if title := episodeTitle(t, db); !strings.Contains(title, "Untitled") {
		t.Fatalf("title = %q, want a plain title", title)
	}
	budgets.mu.Lock()
	defer budgets.mu.Unlock()
	if len(budgets.settled) != 1 || len(budgets.released) != 0 {
		t.Fatalf("budget holds %+v, want one settle for the spent call", budgets)
	}
}

// TestRunRefusesWithoutBudget checks a refused reservation reaches no
// model and stores no proposals.
func TestRunRefusesWithoutBudget(t *testing.T) {
	t.Parallel()
	db := openDiary(t)
	seedForty(t, db)
	model := &scriptedModel{answer: fullAnswer()}
	budgets := &fakeBudget{refuse: cost.ErrOverBudget}
	var receipt []byte
	_, err := editorial.Run(t.Context(), runCfg(db, model, budgets, &receipt))
	if !errors.Is(err, cost.ErrOverBudget) {
		t.Fatalf("error = %v, want ErrOverBudget", err)
	}
	model.mu.Lock()
	calls := model.calls
	model.mu.Unlock()
	if calls != 0 {
		t.Fatalf("model calls = %d, want none after refusal", calls)
	}
	if got := rowCount(t, db, "proposals", "ep-1"); got != 0 {
		t.Fatalf("proposals = %d, want none after refusal", got)
	}
}

// TestRunKeepsProviderCopyOnReceiptFailure checks a receipt store failure
// returns the error with nothing stored and settles the spent call.
func TestRunKeepsProviderCopyOnReceiptFailure(t *testing.T) {
	t.Parallel()
	db := openDiary(t)
	seedForty(t, db)
	model := &scriptedModel{answer: fullAnswer()}
	budgets := &fakeBudget{}
	receiptErr := errors.New("disk full")
	cfg := runCfg(db, model, budgets, &[]byte{})
	cfg.SaveRaw = func(context.Context, []byte) error { return receiptErr }
	_, err := editorial.Run(t.Context(), cfg)
	if !errors.Is(err, receiptErr) {
		t.Fatalf("error = %v, want the receipt failure", err)
	}
	if got := rowCount(t, db, "proposals", "ep-1"); got != 0 {
		t.Fatalf("proposals = %d, want none before the receipt persists", got)
	}
	budgets.mu.Lock()
	defer budgets.mu.Unlock()
	if len(budgets.settled) != 1 || len(budgets.released) != 0 {
		t.Fatalf("budget holds %+v, want one settle for the spent call", budgets)
	}
}

// TestRunSettlesSpentCallOnStoreFailure checks a store failure after a
// billed call still settles the estimate instead of releasing it.
func TestRunSettlesSpentCallOnStoreFailure(t *testing.T) {
	t.Parallel()
	db := openDiary(t)
	seedForty(t, db)
	mustExec(t, db, "DROP TABLE decisions")
	model := &scriptedModel{answer: fullAnswer()}
	budgets := &fakeBudget{}
	var receipt []byte
	if _, err := editorial.Run(t.Context(), runCfg(db, model, budgets, &receipt)); err == nil {
		t.Fatal("run succeeded, want the store failure")
	}
	model.mu.Lock()
	calls := model.calls
	model.mu.Unlock()
	if calls != 1 {
		t.Fatalf("model calls = %d, want one billed call before the failure", calls)
	}
	budgets.mu.Lock()
	defer budgets.mu.Unlock()
	if len(budgets.reserved) != 1 || len(budgets.settled) != 1 || len(budgets.released) != 0 {
		t.Fatalf("budget holds %+v, want one reserve and one settle", budgets)
	}
}

// TestEstimatePricesMinuteRate checks one audio minute costs the minute
// rate and one second rounds up to the nanodollar.
func TestEstimatePricesMinuteRate(t *testing.T) {
	t.Parallel()
	if got := editorial.Estimate(60); got != cost.USD(0.0025) {
		t.Fatalf("minute estimate = %v, want the editorial minute rate", got)
	}
	if got := editorial.Estimate(1); got != cost.Price(41667) {
		t.Fatalf("second estimate = %v, want 41667 nanodollars", got)
	}
	if got := editorial.Estimate(0); got != cost.Price(0) {
		t.Fatalf("zero estimate = %v, want nothing", got)
	}
}

// TestKindNeverResumes pins the job kind: two at once, never idempotent,
// no resume path, one attempt. A restart marks the job interrupted and no
// path repays the call on its own.
func TestKindNeverResumes(t *testing.T) {
	t.Parallel()
	kind := editorial.Kind
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
