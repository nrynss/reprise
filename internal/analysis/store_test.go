package analysis_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/nrynss/reprise/internal/analysis"
)

// TestReplaceStoresRenderedWords pins the stored rows with source
// rendered and leaves the edit timeline untouched.
func TestReplaceStoresRenderedWords(t *testing.T) {
	t.Parallel()
	db := openDiary(t)
	addOwner(t, db, "owner-a")
	addEpisode(t, db, "ep-1", "owner-a", 1, "draft")
	mustExec(t, db,
		"INSERT INTO words (id, owner_id, episode_id, text, start_ms, end_ms, source) VALUES ('w-edit', 'owner-a', 'ep-1', 'draft', 0, 100, 'edit')")
	words := []analysis.Word{
		{Text: "harbour", StartMs: 200, EndMs: 430, Confidence: 0.97},
		{Text: "lantern.", StartMs: 450, EndMs: 700, Confidence: 0.99},
	}
	if err := analysis.ReplaceWords(t.Context(), db, "owner-a", "ep-1", words); err != nil {
		t.Fatalf("replace: %v", err)
	}
	var source string
	if err := db.QueryRowContext(t.Context(),
		"SELECT DISTINCT source FROM words WHERE episode_id = 'ep-1' AND text <> 'draft'").Scan(&source); err != nil {
		t.Fatalf("read source: %v", err)
	}
	if source != "rendered" {
		t.Fatalf("source = %q, want the rendered source", source)
	}
	if source == "edit" {
		t.Fatal("rendered words landed on the edit source the editor reads")
	}
	stored, err := analysis.LoadWords(t.Context(), db, "ep-1")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(stored) != 2 || stored[0].Text != "harbour" || stored[1].StartMs != 450 {
		t.Fatalf("stored = %+v, want both rendered words in order", stored)
	}
	var edits int
	if err := db.QueryRowContext(t.Context(),
		"SELECT COUNT(*) FROM words WHERE episode_id = 'ep-1' AND source = 'edit'").Scan(&edits); err != nil {
		t.Fatalf("count edits: %v", err)
	}
	if edits != 1 {
		t.Fatalf("edit rows = %d, want the untouched draft word", edits)
	}
}

// TestReplaceIsRepeatable checks a second replace stores the same rows
// again instead of doubling them.
func TestReplaceIsRepeatable(t *testing.T) {
	t.Parallel()
	db := openDiary(t)
	addOwner(t, db, "owner-a")
	addEpisode(t, db, "ep-1", "owner-a", 1, "draft")
	words := []analysis.Word{{Text: "one", StartMs: 0, EndMs: 100}}
	for range 2 {
		if err := analysis.ReplaceWords(t.Context(), db, "owner-a", "ep-1", words); err != nil {
			t.Fatalf("replace: %v", err)
		}
	}
	stored, err := analysis.LoadWords(t.Context(), db, "ep-1")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(stored) != 1 {
		t.Fatalf("stored %d words, want 1 after a repeated replace", len(stored))
	}
}

// TestReplaceRejectsInvertedSpan checks a word ending before it starts
// fails the whole replace with the order sentinel.
func TestReplaceRejectsInvertedSpan(t *testing.T) {
	t.Parallel()
	db := openDiary(t)
	addOwner(t, db, "owner-a")
	addEpisode(t, db, "ep-1", "owner-a", 1, "draft")
	words := []analysis.Word{{Text: "bad", StartMs: 300, EndMs: 100}}
	if err := analysis.ReplaceWords(t.Context(), db, "owner-a", "ep-1", words); !errors.Is(err, analysis.ErrOrder) {
		t.Fatalf("replace error = %v, want the order sentinel", err)
	}
}

// TestStoreAnalysisWritesRowAndMentions pins the analyses JSON columns
// and the mention rows with rendered offsets.
func TestStoreAnalysisWritesRowAndMentions(t *testing.T) {
	t.Parallel()
	db := openDiary(t)
	addOwner(t, db, "owner-a")
	addEpisode(t, db, "ep-1", "owner-a", 1, "draft")
	saved := analysis.SavedAnalysis{
		TranscriptID: "tx-1",
		Chapters:     []analysis.Chapter{{Title: "Arrival", StartMs: 0}, {Title: "Crossing", StartMs: 12000}},
		Summary:      analysis.Summary{Headline: "A late crossing", Bullets: []string{"Mara studies charts."}, Block: "Mara studies."},
		Entities:     []analysis.Entity{{Type: "person_name", Text: "Mara", StartMs: 1700, EndMs: 1900}},
		Phrases:      []analysis.KeyPhrase{{Text: "tide charts", Rank: 0.5, Count: 1, Spans: []analysis.Span{{StartMs: 2400, EndMs: 3100}}}},
		Mentions: []analysis.Mention{
			{Kind: "person_name", Offset: 6, Quote: "Mara"},
			{Kind: "keyphrase", Offset: 9, Quote: "tide charts"},
		},
	}
	if err := analysis.StoreAnalysis(t.Context(), db, "owner-a", "ep-1", saved); err != nil {
		t.Fatalf("store: %v", err)
	}
	var chapters, summary, entities, phrases, transcriptID string
	if err := db.QueryRowContext(t.Context(),
		"SELECT transcript_id, chapters, summary, entities, key_phrases FROM analyses WHERE episode_id = 'ep-1'").Scan(
		&transcriptID, &chapters, &summary, &entities, &phrases); err != nil {
		t.Fatalf("read analyses: %v", err)
	}
	if transcriptID != "tx-1" {
		t.Fatalf("transcript id = %q, want tx-1", transcriptID)
	}
	var chapterRows []map[string]any
	if err := json.Unmarshal([]byte(chapters), &chapterRows); err != nil {
		t.Fatalf("decode chapters: %v", err)
	}
	if len(chapterRows) != 2 || chapterRows[0]["title"] != "Arrival" || chapterRows[0]["start_ms"] != float64(0) {
		t.Fatalf("chapters = %v, want both chapters with millisecond starts", chapterRows)
	}
	var summaryRow map[string]any
	if err := json.Unmarshal([]byte(summary), &summaryRow); err != nil {
		t.Fatalf("decode summary: %v", err)
	}
	if summaryRow["headline"] != "A late crossing" {
		t.Fatalf("summary = %v, want the headline stored", summaryRow)
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
	var got []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.kind, &r.offset, &r.quote); err != nil {
			t.Fatalf("scan mention: %v", err)
		}
		got = append(got, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read mentions: %v", err)
	}
	if len(got) != 2 || got[0] != (row{"person_name", 6, "Mara"}) || got[1] != (row{"keyphrase", 9, "tide charts"}) {
		t.Fatalf("mentions = %+v, want both anchors with rendered offsets", got)
	}
}

// TestStoreAnalysisReplacesOnRetry checks a second store swaps the rows
// instead of doubling them.
func TestStoreAnalysisReplacesOnRetry(t *testing.T) {
	t.Parallel()
	db := openDiary(t)
	addOwner(t, db, "owner-a")
	addEpisode(t, db, "ep-1", "owner-a", 1, "draft")
	first := analysis.SavedAnalysis{
		TranscriptID: "tx-1",
		Chapters:     []analysis.Chapter{{Title: "One", StartMs: 0}},
		Mentions:     []analysis.Mention{{Kind: "person_name", Offset: 0, Quote: "Mara"}},
	}
	second := analysis.SavedAnalysis{
		TranscriptID: "tx-2",
		Chapters:     []analysis.Chapter{{Title: "Two", StartMs: 0}},
		Mentions:     []analysis.Mention{{Kind: "person_name", Offset: 1, Quote: "Quilby"}},
	}
	for _, saved := range []analysis.SavedAnalysis{first, second} {
		if err := analysis.StoreAnalysis(t.Context(), db, "owner-a", "ep-1", saved); err != nil {
			t.Fatalf("store: %v", err)
		}
	}
	var analyses, mentions int
	if err := db.QueryRowContext(t.Context(),
		"SELECT COUNT(*) FROM analyses WHERE episode_id = 'ep-1'").Scan(&analyses); err != nil {
		t.Fatalf("count analyses: %v", err)
	}
	if err := db.QueryRowContext(t.Context(),
		"SELECT COUNT(*) FROM mentions WHERE episode_id = 'ep-1'").Scan(&mentions); err != nil {
		t.Fatalf("count mentions: %v", err)
	}
	if analyses != 1 || mentions != 1 {
		t.Fatalf("analyses = %d mentions = %d, want one row each after a retry", analyses, mentions)
	}
	var transcriptID string
	if err := db.QueryRowContext(t.Context(),
		"SELECT transcript_id FROM analyses WHERE episode_id = 'ep-1'").Scan(&transcriptID); err != nil {
		t.Fatalf("read transcript id: %v", err)
	}
	if transcriptID != "tx-2" {
		t.Fatalf("transcript id = %q, want the retry row", transcriptID)
	}
}
