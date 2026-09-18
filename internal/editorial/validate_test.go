package editorial_test

import (
	"strings"
	"testing"

	"github.com/nrynss/reprise/internal/editorial"
)

// TestDecodeRejectsUnknownFields checks a drifted schema fails the parse,
// so new model fields cannot silently slide past validation.
func TestDecodeRejectsUnknownFields(t *testing.T) {
	t.Parallel()
	db := openDiary(t)
	seedForty(t, db)
	model := &scriptedModel{answer: `{"title":"Hi","chapters":[]}`}
	budgets := &fakeBudget{}
	var receipt []byte
	result, err := editorial.Run(t.Context(), runCfg(db, model, budgets, &receipt))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !result.Fallback {
		t.Fatalf("result = %+v, want a fallback for the drifted answer", result)
	}
}

// TestTitleFallsBackWhenMissing checks an answer with valid spans but no
// title stores the spans under a plain title instead of failing.
func TestTitleFallsBackWhenMissing(t *testing.T) {
	t.Parallel()
	db := openDiary(t)
	seedForty(t, db)
	model := &scriptedModel{answer: `{"cold_open":{"start_word":0,"end_word":39,"reason":"warm"},` +
		`"cuts":[],"title":"","show_notes":"Notes.","callback":null}`}
	budgets := &fakeBudget{}
	var receipt []byte
	result, err := editorial.Run(t.Context(), runCfg(db, model, budgets, &receipt))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.Fallback {
		t.Fatalf("result = %+v, want the spans kept with a plain title", result)
	}
	if title := episodeTitle(t, db); !strings.Contains(title, "Untitled") {
		t.Fatalf("title = %q, want a plain title", title)
	}
	if got := rowCount(t, db, "proposals", "ep-1"); got != 3 {
		t.Fatalf("proposals = %d, want cold open, title, and notes", got)
	}
}

// TestShortColdOpenDrops checks a two second opening span is dropped and
// logged while the title still stores.
func TestShortColdOpenDrops(t *testing.T) {
	t.Parallel()
	db := openDiary(t)
	seedForty(t, db)
	model := &scriptedModel{answer: `{"cold_open":{"start_word":0,"end_word":3,"reason":"short"},` +
		`"cuts":[],"title":"Harbor Light","show_notes":"Notes.","callback":null}`}
	budgets := &fakeBudget{}
	var receipt []byte
	log, buf := captureLog()
	cfg := runCfg(db, model, budgets, &receipt)
	cfg.Logger = log
	if _, err := editorial.Run(t.Context(), cfg); err != nil {
		t.Fatalf("run: %v", err)
	}
	if logged := buf.String(); !strings.Contains(logged, "10 to 20") {
		t.Fatalf("log misses the short cold open: %q", logged)
	}
	if got := rowCount(t, db, "proposals", "ep-1"); got != 2 {
		t.Fatalf("proposals = %d, want the title and the notes only", got)
	}
}
