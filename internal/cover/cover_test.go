// Cover pass tests run the whole job offline through the scripted
// double. No test needs a key, a network, or a microphone.

package cover_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nrynss/keel/cost"
	"github.com/nrynss/reprise/internal/cover"
)

// TestRunStoresModelCover pins the model path: the pass briefs the
// model from the stored title and notes, validates the square panel,
// writes the file, stores the row, keeps the receipt, and settles the
// reservation.
func TestRunStoresModelCover(t *testing.T) {
	t.Parallel()
	db := openDiary(t)
	seedEpisode(t, db, "ep-1", "owner-a", 3, "Harbor Light", "We talked about the ferry.")
	model := &cover.ScriptedModel{}
	budgets := &fakeBudget{}
	var receipt []byte
	cfg := runCfg(t, db, model, budgets, &receipt)
	result, err := cover.Run(t.Context(), cfg)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.Fallback {
		t.Fatal("result reports fallback on the model path")
	}
	if result.Width != result.Height || result.Width < 256 {
		t.Fatalf("result measures %dx%d, want a square panel", result.Width, result.Height)
	}
	if model.Calls != 1 || model.Model != "image-model" {
		t.Fatalf("model heard calls=%d model=%q, want one call on the settings model", model.Calls, model.Model)
	}
	if !strings.Contains(model.Req.Prompt, "Harbor Light") || !strings.Contains(model.Req.Prompt, "ferry") {
		t.Fatalf("prompt misses the stored brief: %q", model.Req.Prompt)
	}
	if model.Req.EpisodeNumber != 3 {
		t.Fatalf("request episode = %d, want 3", model.Req.EpisodeNumber)
	}
	if len(receipt) == 0 {
		t.Fatal("receipt is empty")
	}
	file, sha, width, height, fallback := coverRow(t, db, "ep-1")
	if fallback != 0 || width != height {
		t.Fatalf("row holds fallback=%d size=%dx%d, want the model panel square", fallback, width, height)
	}
	if file != "ep-1.png" || sha != result.SHA256 {
		t.Fatalf("row holds file=%q sha=%q, want ep-1.png matching the result", file, sha)
	}
	raw, err := os.ReadFile(filepath.Join(cfg.Dir, file))
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if w, h, err := cover.Validate(raw); err != nil || w != width || h != height {
		t.Fatalf("file validates %dx%d err=%v, want the stored size", w, h, err)
	}
	budgets.mu.Lock()
	defer budgets.mu.Unlock()
	if len(budgets.reserved) != 1 || len(budgets.settled) != 1 || len(budgets.released) != 0 {
		t.Fatalf("budget holds %+v, want one reserve and one settle", budgets)
	}
	want := cover.Estimate()
	if budgets.settled[0] != [2]cost.Price{want, want} {
		t.Fatalf("settled = %v, want {%v %v}", budgets.settled[0], want, want)
	}
}

// TestRunFallsBackOnModelFailure checks a failed call still stores the
// deterministic cover from the episode number, and frees the
// reservation without settling.
func TestRunFallsBackOnModelFailure(t *testing.T) {
	t.Parallel()
	db := openDiary(t)
	seedEpisode(t, db, "ep-1", "owner-a", 7, "Harbor Light", "We talked about the ferry.")
	boom := errors.New("image endpoint unreachable")
	model := &cover.ScriptedModel{Err: boom}
	budgets := &fakeBudget{}
	var receipt []byte
	cfg := runCfg(t, db, model, budgets, &receipt)
	result, err := cover.Run(t.Context(), cfg)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !result.Fallback {
		t.Fatal("result misses the fallback flag after a model failure")
	}
	if result.Width != cover.Size || result.Height != cover.Size {
		t.Fatalf("result measures %dx%d, want the %d square", result.Width, result.Height, cover.Size)
	}
	file, _, width, height, fallback := coverRow(t, db, "ep-1")
	if fallback != 1 || width != cover.Size || height != cover.Size {
		t.Fatalf("row holds fallback=%d size=%dx%d, want the marked fallback square", fallback, width, height)
	}
	raw, err := os.ReadFile(filepath.Join(cfg.Dir, file))
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if w, h, err := cover.Validate(raw); err != nil || w != cover.Size || h != cover.Size {
		t.Fatalf("file validates %dx%d err=%v, want the fallback square", w, h, err)
	}
	budgets.mu.Lock()
	defer budgets.mu.Unlock()
	if len(budgets.reserved) != 1 || len(budgets.settled) != 0 || len(budgets.released) != 1 {
		t.Fatalf("budget holds %+v, want one reserve freed with no settle", budgets)
	}
}

// TestRunRefusesWithoutBudget checks a refused reservation reaches no
// model and stores no cover.
func TestRunRefusesWithoutBudget(t *testing.T) {
	t.Parallel()
	db := openDiary(t)
	seedEpisode(t, db, "ep-1", "owner-a", 3, "Harbor Light", "Notes.")
	model := &cover.ScriptedModel{}
	budgets := &fakeBudget{refuse: cost.ErrOverBudget}
	var receipt []byte
	if _, err := cover.Run(t.Context(), runCfg(t, db, model, budgets, &receipt)); !errors.Is(err, cost.ErrOverBudget) {
		t.Fatalf("error = %v, want ErrOverBudget", err)
	}
	if model.Calls != 0 {
		t.Fatalf("model calls = %d, want none after refusal", model.Calls)
	}
	var count int
	if err := db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM covers").Scan(&count); err != nil {
		t.Fatalf("count covers: %v", err)
	}
	if count != 0 {
		t.Fatalf("covers = %d, want none after refusal", count)
	}
}

// TestRunFallsBackOnRejectedArt checks undecodable, rectangular, and
// flat panels each store the fallback. The call spent, so the
// reservation settles.
func TestRunFallsBackOnRejectedArt(t *testing.T) {
	t.Parallel()
	cases := map[string]func(*testing.T) []byte{
		"bytes": func(t *testing.T) []byte { return []byte("not an image") },
		"rect":  func(t *testing.T) []byte { return rectPNG(t) },
		"flat":  func(t *testing.T) []byte { return solidPNG(t, 512) },
	}
	for name, build := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			db := openDiary(t)
			seedEpisode(t, db, "ep-1", "owner-a", 5, "Harbor Light", "Notes.")
			model := &cover.ScriptedModel{Image: build(t)}
			budgets := &fakeBudget{}
			var receipt []byte
			result, err := cover.Run(t.Context(), runCfg(t, db, model, budgets, &receipt))
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if !result.Fallback {
				t.Fatalf("result misses the fallback flag for %s art", name)
			}
			if _, _, _, _, fallback := coverRow(t, db, "ep-1"); fallback != 1 {
				t.Fatalf("row misses the fallback mark for %s art", name)
			}
			budgets.mu.Lock()
			defer budgets.mu.Unlock()
			if len(budgets.settled) != 1 || len(budgets.released) != 0 {
				t.Fatalf("budget holds %+v, want one settle for the spent call", budgets)
			}
		})
	}
}

// TestRunKeepsProviderCopyOnReceiptFailure checks a receipt store
// failure returns the error with nothing stored and settles the spent
// call.
func TestRunKeepsProviderCopyOnReceiptFailure(t *testing.T) {
	t.Parallel()
	db := openDiary(t)
	seedEpisode(t, db, "ep-1", "owner-a", 3, "Harbor Light", "Notes.")
	model := &cover.ScriptedModel{}
	budgets := &fakeBudget{}
	receiptErr := errors.New("disk full")
	cfg := runCfg(t, db, model, budgets, &[]byte{})
	cfg.SaveRaw = func(context.Context, []byte) error { return receiptErr }
	if _, err := cover.Run(t.Context(), cfg); !errors.Is(err, receiptErr) {
		t.Fatalf("error = %v, want the receipt failure", err)
	}
	var count int
	if err := db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM covers").Scan(&count); err != nil {
		t.Fatalf("count covers: %v", err)
	}
	if count != 0 {
		t.Fatalf("covers = %d, want none before the receipt persists", count)
	}
	budgets.mu.Lock()
	defer budgets.mu.Unlock()
	if len(budgets.settled) != 1 || len(budgets.released) != 0 {
		t.Fatalf("budget holds %+v, want one settle for the spent call", budgets)
	}
}

// TestEveryEpisodeGetsSquareCover seeds three episodes and draws each
// one, so the suite pins the done bar directly.
func TestEveryEpisodeGetsSquareCover(t *testing.T) {
	t.Parallel()
	db := openDiary(t)
	seedEpisode(t, db, "ep-1", "owner-a", 1, "First Light", "Notes one.")
	mustExec(t, db, "INSERT INTO users (id, kind, created_at, last_seen_at) VALUES ('owner-a', 'guest', 1, 2) ON CONFLICT (id) DO NOTHING")
	seedExtra(t, db, "ep-2", 2, "Second Light", "Notes two.")
	seedExtra(t, db, "ep-3", 3, "Third Light", "Notes three.")
	for _, episode := range []string{"ep-1", "ep-2", "ep-3"} {
		model := &cover.ScriptedModel{}
		budgets := &fakeBudget{}
		var receipt []byte
		cfg := runCfg(t, db, model, budgets, &receipt)
		cfg.EpisodeID = episode
		result, err := cover.Run(t.Context(), cfg)
		if err != nil {
			t.Fatalf("run %s: %v", episode, err)
		}
		if result.Width != result.Height {
			t.Fatalf("%s measures %dx%d, want square", episode, result.Width, result.Height)
		}
	}
	var count int
	if err := db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM covers").Scan(&count); err != nil {
		t.Fatalf("count covers: %v", err)
	}
	if count != 3 {
		t.Fatalf("covers = %d, want one per episode", count)
	}
}

// seedExtra seeds another episode under the shared owner.
func seedExtra(t *testing.T, db *sql.DB, id string, number int, title, notes string) {
	t.Helper()
	mustExec(t, db, "INSERT INTO episodes (id, owner_id, number, title, state, visibility, share_token, seeded) VALUES (?, 'owner-a', ?, 'Draft', 'draft', 'private', ?, 0)",
		id, number, "share-"+id)
	mustExec(t, db, "INSERT INTO proposals (id, owner_id, episode_id, kind, start_word, end_word, reason) VALUES (?, 'owner-a', ?, 'title', 0, 0, ?)",
		"prop-title-"+id, id, title)
	mustExec(t, db, "INSERT INTO proposals (id, owner_id, episode_id, kind, start_word, end_word, reason) VALUES (?, 'owner-a', ?, 'show_notes', 0, 0, ?)",
		"prop-notes-"+id, id, notes)
}

// TestRerunReplacesCoverRow runs the pass twice on one episode. The
// second run overwrites the same file and replaces the row, so no
// duplicate survives and the fallback mark stays clear.
func TestRerunReplacesCoverRow(t *testing.T) {
	t.Parallel()
	db := openDiary(t)
	seedEpisode(t, db, "ep-1", "owner-a", 3, "Harbor Light", "We talked about the ferry.")
	model := &cover.ScriptedModel{}
	budgets := &fakeBudget{}
	var receipt []byte
	cfg := runCfg(t, db, model, budgets, &receipt)
	first, err := cover.Run(t.Context(), cfg)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	second, err := cover.Run(t.Context(), cfg)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if second.File != first.File || second.File != "ep-1.png" {
		t.Fatalf("file holds %q after rerun, want %q", second.File, first.File)
	}
	if second.SHA256 != first.SHA256 {
		t.Fatal("rerun stored different bytes for the same brief")
	}
	if model.Calls != 2 {
		t.Fatalf("model calls = %d, want one per run", model.Calls)
	}
	var count int
	if err := db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM covers").Scan(&count); err != nil {
		t.Fatalf("count covers: %v", err)
	}
	if count != 1 {
		t.Fatalf("covers = %d, want one row after rerun", count)
	}
	if file, sha, _, _, fallback := coverRow(t, db, "ep-1"); file != "ep-1.png" || sha != second.SHA256 || fallback != 0 {
		t.Fatalf("row holds file=%q sha=%q fallback=%d, want the replaced model row", file, sha, fallback)
	}
}

// TestPromptConstrainsTheRender checks the brief names the episode and
// refuses faces and lettering, since the app sets the title itself.
func TestPromptConstrainsTheRender(t *testing.T) {
	t.Parallel()
	brief := cover.Prompt("Harbor Light", "We talked about the ferry.")
	lowered := strings.ToLower(brief)
	for _, want := range []string{"harbor light", "ferry", "no faces", "no text", "no letters", "no words"} {
		if !strings.Contains(lowered, strings.ToLower(want)) {
			t.Fatalf("prompt misses %q: %q", want, brief)
		}
	}
}

// TestFallbackDeterministic checks one number draws the same square
// bytes twice, and two numbers draw different panels.
func TestFallbackDeterministic(t *testing.T) {
	t.Parallel()
	first := cover.FallbackImage(7)
	second := cover.FallbackImage(7)
	other := cover.FallbackImage(8)
	if len(first) == 0 || len(second) == 0 || len(other) == 0 {
		t.Fatal("fallback returned no bytes")
	}
	if string(first) != string(second) {
		t.Fatal("one number drew different bytes twice")
	}
	if string(first) == string(other) {
		t.Fatal("two numbers drew the same panel")
	}
	if w, h, err := cover.Validate(first); err != nil || w != cover.Size || h != cover.Size {
		t.Fatalf("fallback validates %dx%d err=%v, want the %d square", w, h, err, cover.Size)
	}
}

// TestValidateRejectsBadPanels checks the validator accepts the
// scripted panel and rejects rectangles and flat art with distinct
// sentinels.
func TestValidateRejectsBadPanels(t *testing.T) {
	t.Parallel()
	model := &cover.ScriptedModel{}
	answer, err := model.GenerateImage(t.Context(), "image-model", cover.ImageRequest{Prompt: "brief"})
	if err != nil {
		t.Fatalf("scripted generate: %v", err)
	}
	if w, h, err := cover.Validate(answer.Image); err != nil || w != h {
		t.Fatalf("scripted panel validates %dx%d err=%v, want square", w, h, err)
	}
	if _, _, err := cover.Validate(rectPNG(t)); !errors.Is(err, cover.ErrNotSquare) {
		t.Fatalf("rect error = %v, want ErrNotSquare", err)
	}
	if _, _, err := cover.Validate(solidPNG(t, 512)); !errors.Is(err, cover.ErrBlank) {
		t.Fatalf("flat error = %v, want ErrBlank", err)
	}
	if _, _, err := cover.Validate([]byte("not an image")); !errors.Is(err, cover.ErrFormat) {
		t.Fatalf("bytes error = %v, want ErrFormat", err)
	}
}

// TestKindNeverResumes pins the job kind: one at a time, never
// idempotent, no resume path, one attempt. A restart marks the job
// interrupted and no path repays the call on its own.
func TestKindNeverResumes(t *testing.T) {
	t.Parallel()
	kind := cover.Kind
	if kind.Limit != 1 {
		t.Fatalf("kind limit = %d, want 1", kind.Limit)
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

// TestEstimatePricesOneImage checks the reservation matches the per
// image figure.
func TestEstimatePricesOneImage(t *testing.T) {
	t.Parallel()
	if got := cover.Estimate(); got != cost.USD(cover.DollarsPerImage) {
		t.Fatalf("estimate = %v, want the per image figure", got)
	}
}
