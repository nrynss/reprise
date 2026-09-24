package analysis

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"

	"github.com/nrynss/keel/id"
	"github.com/nrynss/keel/sqlite"
)

// schemaNamespace is the migration ledger namespace this package owns. It
// shares the database file with the diary tables and the Keel stores
// without colliding, because each namespace keeps its own ledger.
const schemaNamespace = "analysis"

//go:embed migrations/*.sql
var migrations embed.FS

// Migrate applies the analysis schema to db. Call it after the diary
// schema at boot, because the render link references the diary tables. A
// second run changes nothing.
func Migrate(ctx context.Context, db *sqlite.DB) error {
	if db == nil {
		return fmt.Errorf("analysis: migrate: %w: database must not be nil", ErrInvalid)
	}
	schema, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return fmt.Errorf("analysis: migrate: %w", err)
	}
	if err := sqlite.Migrate(ctx, db, schemaNamespace, schema); err != nil {
		return fmt.Errorf("analysis: migrate: %w", err)
	}
	return nil
}

// StoredWord is one persisted rendered word.
type StoredWord struct {
	// ID is the row id ReplaceWords minted.
	ID string
	// Text is the word as heard, with its original casing.
	Text string
	// StartMs is the word start in milliseconds from the render start.
	StartMs int64
	// EndMs is the word end in milliseconds from the render start.
	EndMs int64
}

// ReplaceWords swaps the episode rendered words for words in one
// transaction, and names renderID as the render they came from. A rerun
// writes the same rows again instead of doubling them, so a repeated call
// stays safe. Every row carries SourceRendered, and the edit timeline
// stays untouched. The words and their render land together, so a reader
// never pairs new words with an old render or old words with a new one.
func ReplaceWords(ctx context.Context, db *sql.DB, ownerID, episodeID, renderID string, words []Word) error {
	if db == nil || ownerID == "" || episodeID == "" || renderID == "" {
		return fmt.Errorf("analysis: replace words: %w", ErrInvalid)
	}
	for _, w := range words {
		if w.EndMs < w.StartMs {
			return fmt.Errorf("analysis: replace words %q: %w", w.Text, ErrOrder)
		}
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("analysis: replace words: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		"DELETE FROM words WHERE episode_id = ? AND source = ?", episodeID, SourceRendered); err != nil {
		return fmt.Errorf("analysis: replace words: %w", err)
	}
	for _, w := range words {
		wordID, err := id.New()
		if err != nil {
			return fmt.Errorf("analysis: replace words: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO words (id, owner_id, episode_id, text, start_ms, end_ms, source) VALUES (?, ?, ?, ?, ?, ?, ?)",
			wordID, ownerID, episodeID, w.Text, w.StartMs, w.EndMs, SourceRendered); err != nil {
			return fmt.Errorf("analysis: replace words: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO rendered_sources (episode_id, render_id) VALUES (?, ?)
		 ON CONFLICT(episode_id) DO UPDATE SET render_id = excluded.render_id`,
		episodeID, renderID); err != nil {
		return fmt.Errorf("analysis: replace words: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("analysis: replace words: %w", err)
	}
	return nil
}

// RenderedSource returns the render the stored rendered words came from,
// or empty when no analysis stored words for the episode.
func RenderedSource(ctx context.Context, db *sql.DB, episodeID string) (string, error) {
	if db == nil || episodeID == "" {
		return "", fmt.Errorf("analysis: rendered source: %w", ErrInvalid)
	}
	var renderID string
	err := db.QueryRowContext(ctx,
		"SELECT render_id FROM rendered_sources WHERE episode_id = ?", episodeID).Scan(&renderID)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("analysis: rendered source: %w", err)
	}
	return renderID, nil
}

// LoadWords reads the episode rendered words ordered by start. Chapter
// shaping and mention offsets read the same order the run stored.
func LoadWords(ctx context.Context, db *sql.DB, episodeID string) ([]StoredWord, error) {
	if db == nil || episodeID == "" {
		return nil, fmt.Errorf("analysis: load words: %w", ErrInvalid)
	}
	rows, err := db.QueryContext(ctx,
		"SELECT id, text, start_ms, end_ms FROM words WHERE episode_id = ? AND source = ? ORDER BY start_ms ASC, rowid ASC",
		episodeID, SourceRendered)
	if err != nil {
		return nil, fmt.Errorf("analysis: load words: %w", err)
	}
	defer rows.Close()
	var out []StoredWord
	for rows.Next() {
		var word StoredWord
		if err := rows.Scan(&word.ID, &word.Text, &word.StartMs, &word.EndMs); err != nil {
			return nil, fmt.Errorf("analysis: load words: %w", err)
		}
		out = append(out, word)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("analysis: load words: %w", err)
	}
	return out, nil
}

// SavedAnalysis is one finished analysis ready to store.
type SavedAnalysis struct {
	// TranscriptID is the provider transcript id, deleted after storing.
	TranscriptID string
	// Chapters holds the shaped episode chapters.
	Chapters []Chapter
	// Summary carries the episode notes.
	Summary Summary
	// Entities holds every detected entity for the JSON column.
	Entities []Entity
	// Phrases holds every key phrase for the JSON column.
	Phrases []KeyPhrase
	// Mentions holds the callback anchors with rendered word offsets.
	Mentions []Mention
}

// chapterRow is the stored chapter shape. Exports read timestamped
// lines straight from it.
type chapterRow struct {
	// Title is the chapter title.
	Title string `json:"title"`
	// StartMs is the chapter start in milliseconds from the render start.
	StartMs int64 `json:"start_ms"`
}

// summaryRow is the stored summary shape.
type summaryRow struct {
	// Headline is the one line summary.
	Headline string `json:"headline"`
	// Bullets holds the short summary points.
	Bullets []string `json:"bullets"`
	// Block is the running text summary.
	Block string `json:"block_summary"`
}

// entityRow is the stored entity shape.
type entityRow struct {
	// Type names the entity kind the provider returned.
	Type string `json:"type"`
	// Text is the entity wording as heard.
	Text string `json:"text"`
	// StartMs is the span start in milliseconds from the render start.
	StartMs int64 `json:"start_ms"`
	// EndMs is the span end in milliseconds from the render start.
	EndMs int64 `json:"end_ms"`
}

// spanRow is one stored key phrase occurrence window.
type spanRow struct {
	// StartMs is the occurrence start in milliseconds.
	StartMs int64 `json:"start_ms"`
	// EndMs is the occurrence end in milliseconds.
	EndMs int64 `json:"end_ms"`
}

// phraseRow is the stored key phrase shape.
type phraseRow struct {
	// Text is the phrase wording as heard.
	Text string `json:"text"`
	// Rank scores the phrase importance.
	Rank float64 `json:"rank"`
	// Count carries how often the phrase occurs.
	Count int `json:"count"`
	// Spans holds one window per occurrence.
	Spans []spanRow `json:"spans"`
}

// StoreAnalysis records one finished analysis in one transaction. It
// replaces the episode analyses row and the episode mentions, so an
// explicit retry after a failure stores the same rows again instead of
// doubling them. Callers needing the callback rows built on older
// mentions replant them after a retry.
func StoreAnalysis(ctx context.Context, db *sql.DB, ownerID, episodeID string, saved SavedAnalysis) error {
	if db == nil || ownerID == "" || episodeID == "" {
		return fmt.Errorf("analysis: store analysis: %w", ErrInvalid)
	}
	if saved.TranscriptID == "" {
		return fmt.Errorf("analysis: store analysis: %w: empty transcript id", ErrInvalid)
	}
	chapters, err := marshalChapters(saved.Chapters)
	if err != nil {
		return err
	}
	summary, err := marshalSummary(saved.Summary)
	if err != nil {
		return err
	}
	entities, err := marshalEntities(saved.Entities)
	if err != nil {
		return err
	}
	phrases, err := marshalPhrases(saved.Phrases)
	if err != nil {
		return err
	}
	analysisID, err := id.New()
	if err != nil {
		return fmt.Errorf("analysis: store analysis: %w", err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("analysis: store analysis: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM analyses WHERE episode_id = ?", episodeID); err != nil {
		return fmt.Errorf("analysis: store analysis: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO analyses (id, owner_id, episode_id, transcript_id, chapters, summary, entities, key_phrases)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		analysisID, ownerID, episodeID, saved.TranscriptID, chapters, summary, entities, phrases); err != nil {
		return fmt.Errorf("analysis: store analysis: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM mentions WHERE episode_id = ?", episodeID); err != nil {
		return fmt.Errorf("analysis: store analysis: %w", err)
	}
	for _, m := range saved.Mentions {
		mentionID, err := id.New()
		if err != nil {
			return fmt.Errorf("analysis: store analysis: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO mentions (id, owner_id, episode_id, kind, word_offset, quote) VALUES (?, ?, ?, ?, ?, ?)",
			mentionID, ownerID, episodeID, m.Kind, m.Offset, m.Quote); err != nil {
			return fmt.Errorf("analysis: store analysis: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("analysis: store analysis: %w", err)
	}
	return nil
}

// marshalChapters encodes chapters for the analyses row.
func marshalChapters(chapters []Chapter) (string, error) {
	rows := make([]chapterRow, 0, len(chapters))
	for _, c := range chapters {
		rows = append(rows, chapterRow(c))
	}
	raw, err := json.Marshal(rows)
	if err != nil {
		return "", fmt.Errorf("analysis: encode chapters: %w", err)
	}
	return string(raw), nil
}

// marshalSummary encodes the summary for the analyses row.
func marshalSummary(summary Summary) (string, error) {
	raw, err := json.Marshal(summaryRow(summary))
	if err != nil {
		return "", fmt.Errorf("analysis: encode summary: %w", err)
	}
	return string(raw), nil
}

// marshalEntities encodes entities for the analyses row.
func marshalEntities(entities []Entity) (string, error) {
	rows := make([]entityRow, 0, len(entities))
	for _, e := range entities {
		rows = append(rows, entityRow(e))
	}
	raw, err := json.Marshal(rows)
	if err != nil {
		return "", fmt.Errorf("analysis: encode entities: %w", err)
	}
	return string(raw), nil
}

// marshalPhrases encodes key phrases for the analyses row.
func marshalPhrases(phrases []KeyPhrase) (string, error) {
	rows := make([]phraseRow, 0, len(phrases))
	for _, p := range phrases {
		row := phraseRow{Text: p.Text, Rank: p.Rank, Count: p.Count}
		for _, s := range p.Spans {
			row.Spans = append(row.Spans, spanRow(s))
		}
		rows = append(rows, row)
	}
	raw, err := json.Marshal(rows)
	if err != nil {
		return "", fmt.Errorf("analysis: encode key phrases: %w", err)
	}
	return string(raw), nil
}
