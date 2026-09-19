package memory_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/nrynss/keel/cost"
	"github.com/nrynss/keel/sqlite"
	"github.com/nrynss/reprise/internal/memory"
	"github.com/nrynss/reprise/internal/store"
)

// scriptModel answers marking calls from fixed strings and counts every
// call. A test fails when the package calls what it must not.
type scriptModel struct {
	commitments    string
	commitmentsErr error
	resolution     string
	resolutionErr  error
	commitCalls    int
	resolveCalls   int
	lastTranscript string
	lastCommitment string
}

func (m *scriptModel) GenerateCommitments(ctx context.Context, model string, req memory.CommitmentRequest) (memory.CommitmentAnswer, error) {
	m.commitCalls++
	m.lastTranscript = req.Transcript
	if m.commitmentsErr != nil {
		return memory.CommitmentAnswer{}, m.commitmentsErr
	}
	return memory.CommitmentAnswer{JSON: m.commitments}, nil
}

func (m *scriptModel) GenerateResolution(ctx context.Context, model string, req memory.ResolutionRequest) (memory.ResolutionAnswer, error) {
	m.resolveCalls++
	m.lastCommitment = req.Commitment
	if m.resolutionErr != nil {
		return memory.ResolutionAnswer{}, m.resolutionErr
	}
	return memory.ResolutionAnswer{JSON: m.resolution}, nil
}

// scriptBudget records every reservation move. A refusal error fails the
// reserve, so the test pins that no provider call follows it.
type scriptBudget struct {
	reserveErr error
	reserved   int
	settled    int
	released   int
}

func (b *scriptBudget) Reserve(price cost.Price) error {
	b.reserved++
	return b.reserveErr
}

func (b *scriptBudget) Settle(reserved, actual cost.Price) error {
	b.settled++
	return nil
}

func (b *scriptBudget) Release(price cost.Price) {
	b.released++
}

// openIndex migrates a fresh diary file plus the memory schema and
// returns its writer.
func openIndex(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sqlite.Open(t.Context(), sqlite.Config{
		Path:   filepath.Join(t.TempDir(), "season.db"),
		Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := store.Open(t.Context(), db); err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := memory.Migrate(t.Context(), db); err != nil {
		t.Fatalf("migrate memory: %v", err)
	}
	return db.Writer()
}

// mustExec runs a statement and fails the test on error.
func mustExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, args)
	}
}

// addOwner writes one user row.
func addOwner(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	mustExec(t, db, "INSERT INTO users (id, kind, created_at, last_seen_at) VALUES (?, 'guest', 1, 2)", id)
}

// addEpisode writes one episode row with its number.
func addEpisode(t *testing.T, db *sql.DB, id, owner string, number int) {
	t.Helper()
	mustExec(t, db, "INSERT INTO episodes (id, owner_id, number, title, state, visibility, share_token, seeded) VALUES (?, ?, ?, ?, 'ready', 'private', ?, 1)",
		id, owner, number, fmt.Sprintf("Episode %d", number), "share-"+id)
}

// addWords writes rendered words for one episode with ten millisecond
// steps, so offsets stay exact.
func addWords(t *testing.T, db *sql.DB, owner, episode string, words []string) {
	t.Helper()
	for i, word := range words {
		mustExec(t, db, "INSERT INTO words (id, owner_id, episode_id, text, start_ms, end_ms, source) VALUES (?, ?, ?, ?, ?, ?, 'rendered')",
			fmt.Sprintf("%s-w%d", episode, i), owner, episode, word, int64(i*10), int64(i*10+9))
	}
}

// addMention writes one mention row for the owner.
func addMention(t *testing.T, db *sql.DB, id, owner, episode, kind string, offset int, quote string) {
	t.Helper()
	mustExec(t, db, "INSERT INTO mentions (id, owner_id, episode_id, kind, word_offset, quote) VALUES (?, ?, ?, ?, ?, ?)",
		id, owner, episode, kind, offset, quote)
}

// seedThreads plants people, places, and one key phrase across four
// episodes. Maya recurs in three episodes under two casings. Lisbon
// recurs in two, once with trailing punctuation. The allotment phrase
// spans three episodes, so it circles.
func seedThreads(t *testing.T, db *sql.DB, owner string) {
	t.Helper()
	addOwner(t, db, owner)
	prefix := owner + "-"
	addEpisode(t, db, prefix+"ep1", owner, 1)
	addEpisode(t, db, prefix+"ep2", owner, 2)
	addEpisode(t, db, prefix+"ep3", owner, 3)
	addEpisode(t, db, prefix+"ep4", owner, 4)
	addMention(t, db, prefix+"m1", owner, prefix+"ep1", "person_name", 0, "Maya")
	addMention(t, db, prefix+"m2", owner, prefix+"ep1", "location", 5, "Lisbon")
	addMention(t, db, prefix+"m3", owner, prefix+"ep1", "keyphrase", 10, "the allotment")
	addMention(t, db, prefix+"m4", owner, prefix+"ep2", "person_name", 2, "maya")
	addMention(t, db, prefix+"m5", owner, prefix+"ep2", "keyphrase", 4, "the allotment")
	addMention(t, db, prefix+"m6", owner, prefix+"ep3", "person_name", 1, "Maya")
	addMention(t, db, prefix+"m7", owner, prefix+"ep3", "location", 7, "Lisbon.")
	addMention(t, db, prefix+"m8", owner, prefix+"ep3", "commitment", 12, "I will call Maya back")
	addMention(t, db, prefix+"m9", owner, prefix+"ep4", "keyphrase", 3, "the allotment")
	addMention(t, db, prefix+"m10", owner, prefix+"ep4", "person_name", 9, "Priya")
}

// mentionCount counts mention rows for one episode and kind.
func mentionCount(t *testing.T, db *sql.DB, episode, kind string) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(t.Context(),
		"SELECT COUNT(*) FROM mentions WHERE episode_id = ? AND kind = ?", episode, kind).Scan(&n); err != nil {
		t.Fatalf("count mentions: %v", err)
	}
	return n
}

// TestNormalizeNameFoldsVariants pins the grouping key. Case, edge
// punctuation, and spacing collapse. Inner marks stay.
func TestNormalizeNameFoldsVariants(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"Maya":            "maya",
		"  maya  ":        "maya",
		"Lisbon.":         "lisbon",
		`"Lisbon"`:        "lisbon",
		"The   allotment": "the allotment",
		"well-known":      "well-known",
	}
	for in, want := range cases {
		if got := memory.NormalizeName(in); got != want {
			t.Fatalf("NormalizeName(%q) = %q, want %q", in, got, want)
		}
	}
	if memory.NormalizeName("Maya") != memory.NormalizeName("maya") {
		t.Fatal("casing splits one name into two threads")
	}
}

// TestQuoteVerification pins the contiguous run rule. Reordered words
// and distant words never verify, because a quote must sit in the words
// as said.
func TestQuoteVerification(t *testing.T) {
	t.Parallel()
	words := []string{"I", "will", "call", "Maya", "back", "tomorrow"}
	if !memory.QuoteInWords("will call Maya back", words) {
		t.Fatal("exact run does not verify")
	}
	if !memory.QuoteInWords("Will CALL maya BACK!", words) {
		t.Fatal("casing and punctuation break verification")
	}
	if memory.QuoteInWords("Maya will call", words) {
		t.Fatal("reordered words verify")
	}
	if memory.QuoteInWords("fix the roof", words) {
		t.Fatal("absent quote verifies")
	}
	if memory.QuoteInWords("", words) {
		t.Fatal("blank quote verifies")
	}
	if got := memory.QuoteOffset("call Maya", words); got != 2 {
		t.Fatalf("QuoteOffset = %d, want 2", got)
	}
	if got := memory.QuoteOffset("fix the roof", words); got != -1 {
		t.Fatalf("QuoteOffset = %d, want -1", got)
	}
}

// TestRecurringNamesGroupsPeopleAndPlaces pins the planted threads. Maya
// folds both casings into three episodes. Lisbon folds the dotted
// variant into two. Key phrases and commitments never qualify.
func TestRecurringNamesGroupsPeopleAndPlaces(t *testing.T) {
	t.Parallel()
	db := openIndex(t)
	seedThreads(t, db, "owner-a")
	threads, err := memory.RecurringNames(t.Context(), db, "owner-a", 2)
	if err != nil {
		t.Fatalf("recurring names: %v", err)
	}
	if len(threads) != 2 {
		t.Fatalf("recurring names holds %d threads, want Maya and Lisbon", len(threads))
	}
	maya := threads[0]
	if maya.Key != "maya" || maya.EpisodeCount != 3 || maya.MentionCount != 3 {
		t.Fatalf("Maya thread = %+v, want key maya with 3 mentions in 3 episodes", maya)
	}
	if maya.Display != "Maya" {
		t.Fatalf("Maya display = %q, want the most recent wording", maya.Display)
	}
	for i := 1; i < len(maya.Episodes); i++ {
		if maya.Episodes[i].Number < maya.Episodes[i-1].Number {
			t.Fatal("Maya episodes run out of order")
		}
	}
	lisbon := threads[1]
	if lisbon.Key != "lisbon" || lisbon.EpisodeCount != 2 {
		t.Fatalf("Lisbon thread = %+v, want key lisbon in 2 episodes", lisbon)
	}
	for _, thread := range threads {
		if thread.Key == "the allotment" || thread.Kind == "commitment" {
			t.Fatalf("thread %q should never qualify as a person or place", thread.Key)
		}
	}
}

// TestMarkEpisodeStoresVerifiedRejectsTheRest pins the quote rule end to
// end. The exact quote stores with its offset. The drifting quote never
// reaches the table. A rerun stores the same row again, never a second.
func TestMarkEpisodeStoresVerifiedRejectsTheRest(t *testing.T) {
	t.Parallel()
	db := openIndex(t)
	addOwner(t, db, "owner-a")
	addEpisode(t, db, "owner-a-ep1", "owner-a", 1)
	addWords(t, db, "owner-a", "owner-a-ep1", []string{"I", "will", "call", "Maya", "back", "tomorrow"})
	model := &scriptModel{commitments: `{"commitments": [
		{"quote": "will call Maya back", "text": "call Maya"},
		{"quote": "fix the roof", "text": "fix the roof"}
	]}`}
	budget := &scriptBudget{}
	var receipts [][]byte
	cfg := memory.MarkConfig{
		DB: db, Model: model, Budgets: budget,
		Rates:     memory.Rates{PromptPerMillionUSD: 1, CompletionPerMillionUSD: 1, MaxOutputTokens: 100},
		ModelID:   "mark-model",
		OwnerID:   "owner-a",
		EpisodeID: "owner-a-ep1",
		SaveRaw: func(ctx context.Context, raw []byte) error {
			receipts = append(receipts, raw)
			return nil
		},
	}
	result, err := memory.MarkEpisode(t.Context(), cfg)
	if err != nil {
		t.Fatalf("mark episode: %v", err)
	}
	if len(result.Accepted) != 1 || result.Rejected != 1 {
		t.Fatalf("mark result = %+v, want 1 accepted and 1 rejected", result)
	}
	if result.Accepted[0].Offset != 1 {
		t.Fatalf("accepted offset = %d, want 1", result.Accepted[0].Offset)
	}
	if n := mentionCount(t, db, "owner-a-ep1", "commitment"); n != 1 {
		t.Fatalf("commitment mentions = %d, want the verified one only", n)
	}
	if len(receipts) != 1 {
		t.Fatalf("receipts = %d, want the raw answer persisted", len(receipts))
	}
	if budget.reserved != 1 || budget.settled != 1 || budget.released != 0 {
		t.Fatalf("budget moves = %+v, want one reserve and one settle", budget)
	}
	if _, err := memory.MarkEpisode(t.Context(), cfg); err != nil {
		t.Fatalf("remark episode: %v", err)
	}
	if n := mentionCount(t, db, "owner-a-ep1", "commitment"); n != 1 {
		t.Fatalf("commitment mentions after rerun = %d, want 1", n)
	}
}

// TestMarkEpisodeRefusesBeforeTheCall pins budget first. A refused
// reservation returns before any provider call and stores nothing.
func TestMarkEpisodeRefusesBeforeTheCall(t *testing.T) {
	t.Parallel()
	db := openIndex(t)
	addOwner(t, db, "owner-a")
	addEpisode(t, db, "owner-a-ep1", "owner-a", 1)
	addWords(t, db, "owner-a", "owner-a-ep1", []string{"I", "will", "call"})
	model := &scriptModel{commitments: `{"commitments": []}`}
	budget := &scriptBudget{reserveErr: errors.New("ceiling full")}
	cfg := memory.MarkConfig{
		DB: db, Model: model, Budgets: budget,
		Rates:     memory.Rates{},
		ModelID:   "mark-model",
		OwnerID:   "owner-a",
		EpisodeID: "owner-a-ep1",
		SaveRaw: func(ctx context.Context, raw []byte) error {
			return nil
		},
	}
	if _, err := memory.MarkEpisode(t.Context(), cfg); err == nil {
		t.Fatal("refused reservation marks the episode")
	}
	if model.commitCalls != 0 {
		t.Fatal("provider call runs after a refused reservation")
	}
}

// TestMarkEpisodeNeedsWords pins the transcript requirement. An episode
// with no rendered words fails before any provider call.
func TestMarkEpisodeNeedsWords(t *testing.T) {
	t.Parallel()
	db := openIndex(t)
	addOwner(t, db, "owner-a")
	addEpisode(t, db, "owner-a-ep1", "owner-a", 1)
	model := &scriptModel{commitments: `{"commitments": []}`}
	cfg := memory.MarkConfig{
		DB: db, Model: model, Budgets: &scriptBudget{},
		Rates:     memory.Rates{},
		ModelID:   "mark-model",
		OwnerID:   "owner-a",
		EpisodeID: "owner-a-ep1",
		SaveRaw: func(ctx context.Context, raw []byte) error {
			return nil
		},
	}
	if _, err := memory.MarkEpisode(t.Context(), cfg); !errors.Is(err, memory.ErrNoWords) {
		t.Fatalf("mark without words error = %v, want ErrNoWords", err)
	}
	if model.commitCalls != 0 {
		t.Fatal("provider call runs with no transcript")
	}
}

// TestParseCommitmentsRejectsDrift pins strict decoding. An unknown field
// fails the parse, so a changed shape cannot slide past silently.
func TestParseCommitmentsRejectsDrift(t *testing.T) {
	t.Parallel()
	if _, err := memory.ParseCommitments(`{"commitments": [{"quote": "a", "text": "b", "span": 3}]}`); err == nil {
		t.Fatal("drifted answer parses")
	}
	if _, err := memory.ParseCommitments(`not json`); !errors.Is(err, memory.ErrModel) {
		t.Fatalf("garbage error = %v, want ErrModel", err)
	}
}

// seedResolution plants one commitment in episode one and a later episode
// with words reporting the outcome. The commitment id is fixed, so the
// resolution names a stable row.
func seedResolution(t *testing.T, db *sql.DB) {
	t.Helper()
	addOwner(t, db, "owner-a")
	addEpisode(t, db, "owner-a-ep1", "owner-a", 1)
	addEpisode(t, db, "owner-a-ep2", "owner-a", 2)
	addWords(t, db, "owner-a", "owner-a-ep2", []string{"I", "called", "Maya", "back", "today"})
	addMention(t, db, "owner-a-c1", "owner-a", "owner-a-ep1", "commitment", 3, "I will call Maya back")
}

// TestResolveEpisodeClosesWithEvidence pins the close. The evidence quote
// stores as a doing mention on the later episode, and the commitment
// leaves the open list with its proof attached.
func TestResolveEpisodeClosesWithEvidence(t *testing.T) {
	t.Parallel()
	db := openIndex(t)
	seedResolution(t, db)
	model := &scriptModel{resolution: `{"done": true, "quote": "called Maya back"}`}
	cfg := memory.ResolveConfig{
		DB: db, Model: model, Budgets: &scriptBudget{},
		Rates:        memory.Rates{},
		ModelID:      "mark-model",
		OwnerID:      "owner-a",
		CommitmentID: "owner-a-c1",
		EpisodeID:    "owner-a-ep2",
		SaveRaw: func(ctx context.Context, raw []byte) error {
			return nil
		},
	}
	result, err := memory.ResolveEpisode(t.Context(), cfg)
	if err != nil {
		t.Fatalf("resolve episode: %v", err)
	}
	if !result.Done || result.Evidence != "called Maya back" {
		t.Fatalf("resolve result = %+v, want done with evidence", result)
	}
	if model.lastCommitment != "I will call Maya back" {
		t.Fatalf("model commitment = %q, want the stored quote", model.lastCommitment)
	}
	if n := mentionCount(t, db, "owner-a-ep2", "doing"); n != 1 {
		t.Fatalf("doing mentions = %d, want the evidence row", n)
	}
	open, err := memory.OpenCommitments(t.Context(), db, "owner-a")
	if err != nil {
		t.Fatalf("open commitments: %v", err)
	}
	if len(open) != 0 {
		t.Fatalf("open commitments = %d, want none after the close", len(open))
	}
	resolved, err := memory.ResolvedCommitments(t.Context(), db, "owner-a")
	if err != nil {
		t.Fatalf("resolved commitments: %v", err)
	}
	if len(resolved) != 1 || resolved[0].Evidence != "called Maya back" || resolved[0].EvidenceNumber != 2 {
		t.Fatalf("resolved = %+v, want the evidence with episode 2", resolved)
	}
}

// TestResolveEpisodeRejectsMissingEvidence pins the proof rule. A close
// whose quote misses the later words fails and stores nothing, so the
// commitment stays open.
func TestResolveEpisodeRejectsMissingEvidence(t *testing.T) {
	t.Parallel()
	db := openIndex(t)
	seedResolution(t, db)
	model := &scriptModel{resolution: `{"done": true, "quote": "sailed to Mars"}`}
	cfg := memory.ResolveConfig{
		DB: db, Model: model, Budgets: &scriptBudget{},
		Rates:        memory.Rates{},
		ModelID:      "mark-model",
		OwnerID:      "owner-a",
		CommitmentID: "owner-a-c1",
		EpisodeID:    "owner-a-ep2",
		SaveRaw: func(ctx context.Context, raw []byte) error {
			return nil
		},
	}
	if _, err := memory.ResolveEpisode(t.Context(), cfg); !errors.Is(err, memory.ErrModel) {
		t.Fatalf("resolve error = %v, want ErrModel", err)
	}
	if n := mentionCount(t, db, "owner-a-ep2", "doing"); n != 0 {
		t.Fatalf("doing mentions = %d, want none without proof", n)
	}
	open, err := memory.OpenCommitments(t.Context(), db, "owner-a")
	if err != nil {
		t.Fatalf("open commitments: %v", err)
	}
	if len(open) != 1 {
		t.Fatalf("open commitments = %d, want the still open one", len(open))
	}
}

// TestResolveEpisodeNegativeStoresNothing pins the quiet path. A later
// episode reporting nothing done leaves no rows and still settles.
func TestResolveEpisodeNegativeStoresNothing(t *testing.T) {
	t.Parallel()
	db := openIndex(t)
	seedResolution(t, db)
	model := &scriptModel{resolution: `{"done": false, "quote": ""}`}
	budget := &scriptBudget{}
	cfg := memory.ResolveConfig{
		DB: db, Model: model, Budgets: budget,
		Rates:        memory.Rates{},
		ModelID:      "mark-model",
		OwnerID:      "owner-a",
		CommitmentID: "owner-a-c1",
		EpisodeID:    "owner-a-ep2",
		SaveRaw: func(ctx context.Context, raw []byte) error {
			return nil
		},
	}
	result, err := memory.ResolveEpisode(t.Context(), cfg)
	if err != nil {
		t.Fatalf("resolve episode: %v", err)
	}
	if result.Done {
		t.Fatal("negative answer closes the commitment")
	}
	if budget.settled != 1 || budget.released != 0 {
		t.Fatalf("budget moves = %+v, want one settle", budget)
	}
	if n := mentionCount(t, db, "owner-a-ep2", "doing"); n != 0 {
		t.Fatalf("doing mentions = %d, want none", n)
	}
}

// TestCircledTopics pins the circle rule. The allotment spans three
// episodes with no outcome, so it circles. A phrase in two episodes
// stays out.
func TestCircledTopics(t *testing.T) {
	t.Parallel()
	db := openIndex(t)
	seedThreads(t, db, "owner-a")
	addMention(t, db, "owner-a-x1", "owner-a", "owner-a-ep1", "keyphrase", 20, "cold open picks")
	addMention(t, db, "owner-a-x2", "owner-a", "owner-a-ep2", "keyphrase", 21, "cold open picks")
	topics, err := memory.CircledTopics(t.Context(), db, "owner-a")
	if err != nil {
		t.Fatalf("circled topics: %v", err)
	}
	if len(topics) != 1 {
		t.Fatalf("circled topics = %d, want the allotment only", len(topics))
	}
	topic := topics[0]
	if topic.Key != "the allotment" || topic.EpisodeCount != 3 || topic.MentionCount != 3 {
		t.Fatalf("circled topic = %+v, want the allotment in 3 episodes", topic)
	}
}

// TestCircledTopicsSkipClosedSubjects pins the outcome rule. A phrase
// matching a closed commitment drops out of the circle.
func TestCircledTopicsSkipClosedSubjects(t *testing.T) {
	t.Parallel()
	db := openIndex(t)
	seedThreads(t, db, "owner-a")
	addMention(t, db, "owner-a-x1", "owner-a", "owner-a-ep1", "keyphrase", 20, "I will call Maya back")
	addMention(t, db, "owner-a-x2", "owner-a", "owner-a-ep2", "keyphrase", 21, "I will call Maya back")
	addMention(t, db, "owner-a-x3", "owner-a", "owner-a-ep4", "keyphrase", 22, "I will call Maya back")
	addMention(t, db, "owner-a-c2", "owner-a", "owner-a-ep1", "commitment", 30, "I will call Maya back")
	// The test closes the commitment directly, because the close rule is
	// pinned elsewhere. The circle only reads the outcome.
	if err := memory.StoreResolution(t.Context(), db, "owner-a", "owner-a-c2", "owner-a-ep2", "called Maya back", 1); err != nil {
		t.Fatalf("store resolution: %v", err)
	}
	topics, err := memory.CircledTopics(t.Context(), db, "owner-a")
	if err != nil {
		t.Fatalf("circled topics: %v", err)
	}
	for _, topic := range topics {
		if topic.Key == "i will call maya back" {
			t.Fatal("closed subject still circles")
		}
	}
}

// TestKeytermsRanksRecentAndFrequent pins the boost order. The allotment
// and Maya tie on count, so the newer episode leads. Lisbon follows on
// count.
func TestKeytermsRanksRecentAndFrequent(t *testing.T) {
	t.Parallel()
	db := openIndex(t)
	seedThreads(t, db, "owner-a")
	terms, err := memory.Keyterms(t.Context(), db, "owner-a", 0)
	if err != nil {
		t.Fatalf("keyterms: %v", err)
	}
	if len(terms) < 3 || terms[0].Term != "the allotment" || terms[0].Count != 3 {
		t.Fatalf("keyterms lead = %+v, want the allotment with count 3", terms)
	}
	if terms[1].Term != "Maya" || terms[1].Count != 3 {
		t.Fatalf("keyterms second = %+v, want Maya with count 3", terms)
	}
	capped, err := memory.Keyterms(t.Context(), db, "owner-a", 2)
	if err != nil {
		t.Fatalf("capped keyterms: %v", err)
	}
	if len(capped) != 2 {
		t.Fatalf("capped keyterms = %d, want 2", len(capped))
	}
}

// TestCountMentions pins the spoken arithmetic. Maya shows three
// mentions across episodes one to three. An unknown name answers zeros.
func TestCountMentions(t *testing.T) {
	t.Parallel()
	db := openIndex(t)
	seedThreads(t, db, "owner-a")
	counts, err := memory.CountMentions(t.Context(), db, "owner-a", "MAYA")
	if err != nil {
		t.Fatalf("count mentions: %v", err)
	}
	if counts.Mentions != 3 || counts.Episodes != 3 || counts.FirstEpisode != 1 || counts.LastEpisode != 3 {
		t.Fatalf("counts = %+v, want 3 mentions in episodes 1 to 3", counts)
	}
	empty, err := memory.CountMentions(t.Context(), db, "owner-a", "nobody here")
	if err != nil {
		t.Fatalf("count unknown: %v", err)
	}
	if empty != (memory.Counts{}) {
		t.Fatalf("unknown counts = %+v, want zeros", empty)
	}
}

// TestMigrateRunsTwiceWithoutChange pins the ledger. A second migrate
// changes nothing, so a second boot stays quiet.
func TestMigrateRunsTwiceWithoutChange(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "season.db")
	db, err := sqlite.Open(t.Context(), sqlite.Config{
		Path:   path,
		Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := store.Open(t.Context(), db); err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := memory.Migrate(t.Context(), db); err != nil {
		t.Fatalf("migrate memory: %v", err)
	}
	if err := memory.Migrate(t.Context(), db); err != nil {
		t.Fatalf("remigrate memory: %v", err)
	}
	var n int
	if err := db.Writer().QueryRowContext(t.Context(),
		"SELECT COUNT(*) FROM resolutions").Scan(&n); err != nil {
		t.Fatalf("resolutions table missing after migrate: %v", err)
	}
}

// TestEpisodeDeleteCascadesResolutions pins the cleanup. Deleting the
// episode removes its mentions and their resolutions, so erased history
// leaves no index behind.
func TestEpisodeDeleteCascadesResolutions(t *testing.T) {
	t.Parallel()
	db := openIndex(t)
	seedResolution(t, db)
	if err := memory.StoreResolution(t.Context(), db, "owner-a", "owner-a-c1", "owner-a-ep2", "called Maya back", 1); err != nil {
		t.Fatalf("store resolution: %v", err)
	}
	mustExec(t, db, "DELETE FROM episodes WHERE id = 'owner-a-ep1'")
	mustExec(t, db, "DELETE FROM episodes WHERE id = 'owner-a-ep2'")
	var n int
	if err := db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM resolutions").Scan(&n); err != nil {
		t.Fatalf("count resolutions: %v", err)
	}
	if n != 0 {
		t.Fatalf("resolutions = %d after episode delete, want 0", n)
	}
}

// TestMigrateRejectsNilDatabase pins the guard.
func TestMigrateRejectsNilDatabase(t *testing.T) {
	t.Parallel()
	if err := memory.Migrate(t.Context(), nil); !errors.Is(err, memory.ErrInvalid) {
		t.Fatalf("migrate nil error = %v, want ErrInvalid", err)
	}
}
// TestStoreResolutionRejectsBackdatedClose pins the later episode rule.
// A commitment from episode two never closes on episode one, and the
// failed close stores nothing.
func TestStoreResolutionRejectsBackdatedClose(t *testing.T) {
	t.Parallel()
	db := openIndex(t)
	addOwner(t, db, "owner-a")
	addEpisode(t, db, "owner-a-ep1", "owner-a", 1)
	addEpisode(t, db, "owner-a-ep2", "owner-a", 2)
	addMention(t, db, "owner-a-c1", "owner-a", "owner-a-ep2", "commitment", 0, "I will call Maya back")
	if err := memory.StoreResolution(t.Context(), db, "owner-a", "owner-a-c1", "owner-a-ep1", "called Maya back", 0); !errors.Is(err, memory.ErrInvalid) {
		t.Fatalf("backdated close error = %v, want ErrInvalid", err)
	}
	if n := mentionCount(t, db, "owner-a-ep1", "doing"); n != 0 {
		t.Fatalf("doing mentions = %d, want none after a backdated close", n)
	}
	var n int
	if err := db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM resolutions").Scan(&n); err != nil {
		t.Fatalf("count resolutions: %v", err)
	}
	if n != 0 {
		t.Fatalf("resolutions = %d, want none after a backdated close", n)
	}
}

// TestStoreResolutionRejectsSameEpisodeClose pins the strict order. The
// evidence must land later, so closing on the commitment episode fails.
func TestStoreResolutionRejectsSameEpisodeClose(t *testing.T) {
	t.Parallel()
	db := openIndex(t)
	addOwner(t, db, "owner-a")
	addEpisode(t, db, "owner-a-ep1", "owner-a", 1)
	addMention(t, db, "owner-a-c1", "owner-a", "owner-a-ep1", "commitment", 0, "I will call Maya back")
	if err := memory.StoreResolution(t.Context(), db, "owner-a", "owner-a-c1", "owner-a-ep1", "called Maya back", 0); !errors.Is(err, memory.ErrInvalid) {
		t.Fatalf("same episode close error = %v, want ErrInvalid", err)
	}
}

// TestResolveEpisodeRejectsBackdatedEpisode pins the model path order
// check. A backdated run fails before any provider call and stores
// nothing, so an invalid request never spends budget.
func TestResolveEpisodeRejectsBackdatedEpisode(t *testing.T) {
	t.Parallel()
	db := openIndex(t)
	addOwner(t, db, "owner-a")
	addEpisode(t, db, "owner-a-ep1", "owner-a", 1)
	addEpisode(t, db, "owner-a-ep2", "owner-a", 2)
	addWords(t, db, "owner-a", "owner-a-ep1", []string{"I", "called", "Maya", "back", "today"})
	addMention(t, db, "owner-a-c1", "owner-a", "owner-a-ep2", "commitment", 0, "I will call Maya back")
	model := &scriptModel{resolution: `{"done": true, "quote": "called Maya back"}`}
	budget := &scriptBudget{}
	cfg := memory.ResolveConfig{
		DB: db, Model: model, Budgets: budget,
		Rates:        memory.Rates{},
		ModelID:      "mark-model",
		OwnerID:      "owner-a",
		CommitmentID: "owner-a-c1",
		EpisodeID:    "owner-a-ep1",
		SaveRaw: func(ctx context.Context, raw []byte) error {
			return nil
		},
	}
	if _, err := memory.ResolveEpisode(t.Context(), cfg); !errors.Is(err, memory.ErrInvalid) {
		t.Fatalf("backdated resolve error = %v, want ErrInvalid", err)
	}
	if model.resolveCalls != 0 {
		t.Fatal("provider call runs for a backdated resolve")
	}
	if budget.reserved != 0 {
		t.Fatal("budget reserves for a backdated resolve")
	}
	if n := mentionCount(t, db, "owner-a-ep1", "doing"); n != 0 {
		t.Fatalf("doing mentions = %d, want none after a backdated resolve", n)
	}
}

