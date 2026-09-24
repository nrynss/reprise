package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nrynss/keel/job"
	jobsqlitestore "github.com/nrynss/keel/job/sqlitestore"
	"github.com/nrynss/keel/stream"
	"github.com/nrynss/reprise/internal/analysis"
	"github.com/nrynss/reprise/internal/api"
	"github.com/nrynss/reprise/internal/assemblyai"
	"github.com/nrynss/reprise/internal/broker"
	"github.com/nrynss/reprise/internal/episode"
	"github.com/nrynss/reprise/internal/gemini"
	"github.com/nrynss/reprise/internal/identity"
	"github.com/nrynss/reprise/internal/render"
	"github.com/nrynss/reprise/internal/retention"
	"google.golang.org/genai"
)

// finishCommitment is the quote the scripted marking model returns. The
// scripted transcript holds it, so verification keeps it.
const finishCommitment = "call my sister"

// finishGemini answers every model call the finish chain makes from
// canned bodies. Chapters and marking return the JSON their schemas
// promise. The image call fails when drawFails is set, and otherwise
// returns bytes the validator rejects, so the cover pass stores its
// deterministic panel either way.
func finishGemini(drawFails bool) *gemini.Client {
	return gemini.NewTestClient(func(_ context.Context, _ string, contents []*genai.Content, config *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
		for _, modality := range config.ResponseModalities {
			if modality == "IMAGE" {
				if drawFails {
					return nil, errors.New("scripted image refusal")
				}
				return &genai.GenerateContentResponse{
					Candidates: []*genai.Candidate{{
						Content: &genai.Content{Role: "model", Parts: []*genai.Part{{
							InlineData: &genai.Blob{MIMEType: "image/png", Data: []byte{0x89, 0x50, 0x4e, 0x47}},
						}}},
					}},
				}, nil
			}
		}
		var prompt string
		if len(contents) > 0 && len(contents[0].Parts) > 0 {
			prompt = contents[0].Parts[0].Text
		}
		body := `{"title":"Harbor Light"}`
		switch {
		case strings.Contains(prompt, "Chapter the transcript"):
			body = `{"chapters":[{"title":"Dawn Ferry","start_ms":0}]}`
		case strings.Contains(prompt, "List the concrete things"):
			body = `{"commitments":[{"quote":"` + finishCommitment + `","text":"Call my sister"}]}`
		}
		return &genai.GenerateContentResponse{
			Candidates: []*genai.Candidate{{
				Content:      &genai.Content{Role: "model", Parts: []*genai.Part{{Text: body}}},
				FinishReason: genai.FinishReasonStop,
			}},
			UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
				PromptTokenCount: 12, CandidatesTokenCount: 5, TotalTokenCount: 17,
			},
		}, nil
	})
}

// finishTranscriber answers the render batch pass from a canned
// transcript that holds the commitment quote, with no network.
func finishTranscriber() *analysis.ScriptedTranscriber {
	texts := []string{"I", "will", "call", "my", "sister", "tomorrow."}
	words := make([]analysis.Word, 0, len(texts))
	for i, text := range texts {
		start := int64(i * 150)
		words = append(words, analysis.Word{Text: text, StartMs: start, EndMs: start + 120, Confidence: 0.98})
	}
	return &analysis.ScriptedTranscriber{
		Completion: analysis.TranscriptResult{
			ID:                "tx-finish",
			Text:              "I will call my sister tomorrow.",
			Words:             words,
			AudioDurationSecs: 1,
			Raw:               []byte(`{"id":"tx-finish"}`),
		},
		Deleted: analysis.TranscriptResult{ID: "tx-finish", Text: assemblyai.DeletedText},
	}
}

// useFinishModels rebinds the fixture pipeline to the finish chain doubles
// and returns the transcriber, so a test can fail the batch pass.
func useFinishModels(fx *wireFixture, drawFails bool) *analysis.ScriptedTranscriber {
	p := fx.pipe
	fx.pipe = newPipeline(p.db, finishGemini(drawFails), p.batch, p.budgets, p.ownerLimit, p.editorialModel,
		p.media, p.mediaDir, p.coverDir, p.renderWorkDir)
	scripted := finishTranscriber()
	fx.pipe.transcriber = scripted
	return scripted
}

// bootFinishJobs boots the job wiring the binary boots, over the fixture
// stores, with the finish chain doubles and no network. extra carries
// feature kinds the boot merges.
func bootFinishJobs(t *testing.T, fx *wireFixture, extra ...map[string]job.Kind) *jobs {
	t.Helper()
	j, err := tryBootFinishJobs(t, fx, extra...)
	if err != nil {
		t.Fatalf("boot jobs: %v", err)
	}
	t.Cleanup(func() { waitIdle(t, fx) })
	return j
}

// tryBootFinishJobs boots the job wiring and reports the boot error.
func tryBootFinishJobs(t *testing.T, fx *wireFixture, extra ...map[string]job.Kind) (*jobs, error) {
	t.Helper()
	resolver := &render.Resolver{
		DB:      fx.db.Writer(),
		Media:   render.StoreMedia(fx.media, fx.index),
		WorkDir: fx.pipe.renderWorkDir,
		Locate:  fx.pipe.locateStems,
	}
	rec := openTestReconciler(t, fx, fakeDiary{db: fx.db}, fakeLeaseSettler{})
	sweeper, err := broker.NewSweeper(broker.SweeperConfig{
		DB: fx.db, Source: stubSource{}, Statuses: fakeStatuses{},
		Ender: fakeEnder{}, Reconciler: rec,
	})
	if err != nil {
		t.Fatalf("open sweeper: %v", err)
	}
	return startJobs(t.Context(), jobsConfig{
		db:       fx.db,
		events:   stream.New(stream.Config{}),
		pipe:     fx.pipe,
		resolver: resolver,
		rec:      rec,
		sweeper:  sweeper,
		extra:    extra,
	})
}

// waitIdle waits until no job runs or waits, so a test never ends while
// a chain still writes. The deadline only stops a hung chain from
// hanging the suite. No assertion reads elapsed time.
func waitIdle(t *testing.T, fx *wireFixture) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for {
		var live int
		if err := fx.db.Reader().QueryRowContext(context.Background(),
			`SELECT COUNT(*) FROM jobs WHERE status IN (?, ?)`,
			string(job.StatusRunning), string(job.StatusQueued)).Scan(&live); err != nil {
			t.Errorf("count live jobs: %v", err)
			return
		}
		if live == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Errorf("jobs still live: %d", live)
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// waitState waits until the episode reads want and every job stopped.
func waitState(t *testing.T, fx *wireFixture, episodeID string, want episode.State) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for finishState(t, fx, episodeID) != want {
		if time.Now().After(deadline) {
			t.Fatalf("episode state = %s, want %s", finishState(t, fx, episodeID), want)
		}
		time.Sleep(10 * time.Millisecond)
	}
	waitIdle(t, fx)
}

// finishState reads one episode state and fails the test on error.
func finishState(t *testing.T, fx *wireFixture, episodeID string) episode.State {
	t.Helper()
	state, err := episode.Current(context.Background(), fx.db, episodeID)
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	return state
}

// kindJobsFor counts the jobs of one kind stamped for one episode.
func kindJobsFor(t *testing.T, fx *wireFixture, kind, episodeID string) int {
	t.Helper()
	var count int
	if err := fx.db.Reader().QueryRowContext(t.Context(),
		`SELECT COUNT(*) FROM jobs WHERE kind = ? AND progress LIKE ? ESCAPE '\'`,
		kind, `%"episode_id":"`+episodeID+`"%`).Scan(&count); err != nil {
		t.Fatalf("count %s jobs: %v", kind, err)
	}
	return count
}

// plantRunningJob stores one job a dead process left running, stamped
// with its linkage.
func plantRunningJob(t *testing.T, fx *wireFixture, id, kind string, desc episodeDescriptor) {
	t.Helper()
	store := openFixtureJobStore(t, fx)
	detail, err := json.Marshal(desc)
	if err != nil {
		t.Fatalf("encode linkage: %v", err)
	}
	if err := store.Create(t.Context(), job.Record{
		ID: id, Kind: kind, Status: job.StatusRunning, Attempt: 1, RootID: id,
		Progress:  job.Progress{Stage: "start", Detail: detail},
		UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("create running job: %v", err)
	}
}

// openFixtureJobStore opens the durable job rows on the fixture database.
func openFixtureJobStore(t *testing.T, fx *wireFixture) *jobsqlitestore.Store {
	t.Helper()
	store, err := jobsqlitestore.Open(t.Context(), jobsqlitestore.Config{DB: fx.db})
	if err != nil {
		t.Fatalf("open job store: %v", err)
	}
	return store
}

// countRows counts rows one query matches and fails the test on error.
func countRows(t *testing.T, fx *wireFixture, query string, args ...any) int {
	t.Helper()
	var n int
	if err := fx.db.Reader().QueryRowContext(t.Context(), query, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return n
}

// finishGuest mints one guest through the identity middleware and returns
// its cookie and id.
func finishGuest(t *testing.T, guests *identity.Service) (*http.Cookie, string) {
	t.Helper()
	var owner string
	wrapped := guests.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if user, ok := identity.UserFromContext(r.Context()); ok {
			owner = user.ID
		}
	}))
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	for _, c := range rec.Result().Cookies() {
		if c.Name == identity.CookieName && owner != "" {
			return c, owner
		}
	}
	t.Fatal("mint set no guest session")
	return nil, ""
}

// finishDraft creates one draft episode for owner with both stems, an
// edit timeline, and a stored title, the state the editorial pass
// leaves. It returns the episode id.
func finishDraft(t *testing.T, fx *wireFixture, owner string) string {
	t.Helper()
	episodeID := draftEpisode(t, fx, owner)
	seedEditWords(t, fx, owner, episodeID)
	if _, err := fx.db.Writer().ExecContext(t.Context(),
		`INSERT INTO proposals (id, owner_id, episode_id, kind, start_word, end_word, reason)
		 VALUES (?, ?, ?, 'title', 0, 0, 'Harbor Light')`, "title-"+episodeID, owner, episodeID); err != nil {
		t.Fatalf("seed title: %v", err)
	}
	if _, err := fx.db.Writer().ExecContext(t.Context(),
		`UPDATE episodes SET state = ?, title = 'Harbor Light' WHERE id = ?`,
		string(episode.StateDraft), episodeID); err != nil {
		t.Fatalf("plant draft: %v", err)
	}
	return episodeID
}

// finishRoutes returns the episode routes over the wired service, behind
// the guest middleware the binary mounts them behind.
func finishRoutes(t *testing.T, fx *wireFixture, j *jobs) (http.Handler, *identity.Service) {
	t.Helper()
	guests, err := identity.New(t.Context(), identity.Config{DB: fx.db, SigningKey: "finish-test-signing-key"})
	if err != nil {
		t.Fatalf("open identity: %v", err)
	}
	svc, err := episode.NewService(episodeConfig(fx.db, j))
	if err != nil {
		t.Fatalf("new episode service: %v", err)
	}
	return guests.Middleware(api.NewEpisodes(svc)), guests
}

// postDone marks one episode done through the routes and returns the
// status.
func postDone(t *testing.T, routes http.Handler, cookie *http.Cookie, episodeID string) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/episodes/"+episodeID+"/done", bytes.NewReader(nil))
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	routes.ServeHTTP(rec, req)
	return rec.Code
}

// postDoneBody marks one episode done through the routes and returns the
// status with the body.
func postDoneBody(t *testing.T, routes http.Handler, cookie *http.Cookie, episodeID string) (int, []byte) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/episodes/"+episodeID+"/done", bytes.NewReader(nil))
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	routes.ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes()
}

// finishDetail reads one episode detail through the routes.
func finishDetail(t *testing.T, routes http.Handler, cookie *http.Cookie, episodeID string) map[string]json.RawMessage {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/episodes/"+episodeID, nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	routes.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("detail status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	return body
}

// passOf decodes one pass outcome from a detail body.
func passOf(t *testing.T, body map[string]json.RawMessage, field string) transcriptOutcomeJSON {
	t.Helper()
	raw, ok := body[field]
	if !ok {
		t.Fatalf("detail carries no %s", field)
	}
	var out transcriptOutcomeJSON
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode %s: %v", field, err)
	}
	return out
}

// TestMarkDoneReachesReadyOffline marks a fixture draft done through the
// wired routes and job chain with no network. The render runs through
// ffmpeg, the batch pass and the models answer from doubles, and the
// episode reaches ready. Its chapters, cover, and commitments are stored
// rows. The rendered words name the render, and the detail reports every
// pass done. A second mark done and a repeated render completion start
// no second chain.
func TestMarkDoneReachesReadyOffline(t *testing.T) {
	fx := openWireFixture(t)
	scripted := useFinishModels(fx, false)
	j := bootFinishJobs(t, fx)
	routes, guests := finishRoutes(t, fx, j)
	cookie, owner := finishGuest(t, guests)
	episodeID := finishDraft(t, fx, owner)

	if code := postDone(t, routes, cookie, episodeID); code != http.StatusAccepted {
		t.Fatalf("mark done status = %d, want 202", code)
	}
	waitState(t, fx, episodeID, episode.StateReady)

	var chapters string
	if err := fx.db.Reader().QueryRowContext(t.Context(),
		`SELECT chapters FROM analyses WHERE episode_id = ?`, episodeID).Scan(&chapters); err != nil {
		t.Fatalf("read chapters: %v", err)
	}
	if !strings.Contains(chapters, "Dawn Ferry") {
		t.Fatalf("chapters = %s, want the scripted chapter", chapters)
	}
	if n := countRows(t, fx, `SELECT COUNT(*) FROM covers WHERE episode_id = ?`, episodeID); n != 1 {
		t.Fatalf("covers = %d, want one stored cover", n)
	}
	var quote string
	if err := fx.db.Reader().QueryRowContext(t.Context(),
		`SELECT quote FROM mentions WHERE episode_id = ? AND kind = 'commitment'`, episodeID).Scan(&quote); err != nil {
		t.Fatalf("read commitment: %v", err)
	}
	if quote != finishCommitment {
		t.Fatalf("commitment = %q, want %q", quote, finishCommitment)
	}
	newest, err := episode.NewestRenderID(t.Context(), fx.db, episodeID)
	if err != nil || newest == "" {
		t.Fatalf("newest render = %q, %v, want one render", newest, err)
	}
	source, err := analysis.RenderedSource(t.Context(), fx.db.Reader(), episodeID)
	if err != nil {
		t.Fatalf("rendered source: %v", err)
	}
	if source != newest {
		t.Fatalf("rendered source = %q, want the newest render %q", source, newest)
	}
	if scripted.Creates != 1 {
		t.Fatalf("batch creates = %d, want one analysis", scripted.Creates)
	}
	for _, kind := range []string{kindRender, kindAnalysis, kindCover, kindMemory} {
		if n := kindJobsFor(t, fx, kind, episodeID); n != 1 {
			t.Fatalf("%s jobs = %d, want one", kind, n)
		}
	}
	if held := wireHeld(t, fx); held != 0 {
		t.Fatalf("held = %s, want every reservation settled or released", held)
	}

	body := finishDetail(t, routes, cookie, episodeID)
	for _, field := range []string{"render_outcome", "analysis_outcome", "memory_outcome"} {
		if pass := passOf(t, body, field); pass.Status != string(job.StatusDone) {
			t.Fatalf("%s = %+v, want done", field, pass)
		}
	}
	var renderWords []wordJSONProbe
	if err := json.Unmarshal(body["render_words"], &renderWords); err != nil {
		t.Fatalf("decode render words: %v", err)
	}
	if len(renderWords) != 6 || renderWords[2].Text != "call" {
		t.Fatalf("render words = %+v, want the analysed words of the newest render", renderWords)
	}

	if code := postDone(t, routes, cookie, episodeID); code != http.StatusConflict {
		t.Fatalf("second mark done status = %d, want 409", code)
	}
	renderJob, err := episode.LastKindJob(t.Context(), fx.db, episodeID, kindRender)
	if err != nil || !renderJob.Found {
		t.Fatalf("render job = %+v, %v, want the one render", renderJob, err)
	}
	rec, err := j.store.Get(t.Context(), renderJob.JobID)
	if err != nil {
		t.Fatalf("read render job: %v", err)
	}
	if _, err := j.settleRender(t.Context(), owner, episodeID, rec.Data, nil); err != nil {
		t.Fatalf("repeat render completion: %v", err)
	}
	j.advanceAll(t.Context())
	waitIdle(t, fx)
	for _, kind := range []string{kindRender, kindAnalysis, kindCover, kindMemory} {
		if n := kindJobsFor(t, fx, kind, episodeID); n != 1 {
			t.Fatalf("%s jobs after repeats = %d, want still one", kind, n)
		}
	}
	if n := countRows(t, fx, `SELECT COUNT(*) FROM renders WHERE episode_id = ?`, episodeID); n != 1 {
		t.Fatalf("renders = %d, want one", n)
	}
	if got := finishState(t, fx, episodeID); got != episode.StateReady {
		t.Fatalf("state after repeats = %s, want ready", got)
	}
}

// wordJSONProbe decodes one rendered word from the detail body.
type wordJSONProbe struct {
	Text  string  `json:"text"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

// TestFailedAnalysisStillShips fails the batch pass. The episode still
// reaches ready with its plain title and its cover, no marking pass
// starts, and the analysis reservation is released.
func TestFailedAnalysisStillShips(t *testing.T) {
	fx := openWireFixture(t)
	scripted := useFinishModels(fx, false)
	scripted.WaitErr = errors.New("scripted batch refusal")
	j := bootFinishJobs(t, fx)
	routes, guests := finishRoutes(t, fx, j)
	cookie, owner := finishGuest(t, guests)
	episodeID := draftEpisode(t, fx, owner)
	seedEditWords(t, fx, owner, episodeID)
	if _, err := fx.db.Writer().ExecContext(t.Context(),
		`UPDATE episodes SET state = ? WHERE id = ?`, string(episode.StateDraft), episodeID); err != nil {
		t.Fatalf("plant draft: %v", err)
	}
	var plain string
	if err := fx.db.Reader().QueryRowContext(t.Context(),
		`SELECT title FROM episodes WHERE id = ?`, episodeID).Scan(&plain); err != nil {
		t.Fatalf("read title: %v", err)
	}

	if code := postDone(t, routes, cookie, episodeID); code != http.StatusAccepted {
		t.Fatalf("mark done status = %d, want 202", code)
	}
	waitState(t, fx, episodeID, episode.StateReady)

	var title string
	if err := fx.db.Reader().QueryRowContext(t.Context(),
		`SELECT title FROM episodes WHERE id = ?`, episodeID).Scan(&title); err != nil {
		t.Fatalf("read title: %v", err)
	}
	if title != plain || !strings.HasPrefix(title, "Episode ") {
		t.Fatalf("title = %q, want the plain title %q", title, plain)
	}
	if n := kindJobsFor(t, fx, kindMemory, episodeID); n != 0 {
		t.Fatalf("marking jobs = %d, want none after a failed analysis", n)
	}
	if n := countRows(t, fx, `SELECT COUNT(*) FROM covers WHERE episode_id = ?`, episodeID); n != 1 {
		t.Fatalf("covers = %d, want the cover beside a failed analysis", n)
	}
	body := finishDetail(t, routes, cookie, episodeID)
	if pass := passOf(t, body, "analysis_outcome"); pass.Status != string(job.StatusError) || pass.Error == "" {
		t.Fatalf("analysis outcome = %+v, want the failed pass with its reason", pass)
	}
	if audio := string(body["render_audio_url"]); !strings.Contains(audio, "/media/") {
		t.Fatalf("render address = %s, want the playable render", audio)
	}
	if held := wireHeld(t, fx); held != 0 {
		t.Fatalf("held = %s, want the analysis hold released", held)
	}
}

// TestCoverFailureLeavesPlainCover settles a cover pass that failed
// before it stored anything. The plain cover lands, and the episode still
// reaches ready once analysis and marking finish.
func TestCoverFailureLeavesPlainCover(t *testing.T) {
	fx := openWireFixture(t)
	useFinishModels(fx, true)
	j := bootFinishJobs(t, fx)
	routes, guests := finishRoutes(t, fx, j)
	cookie, owner := finishGuest(t, guests)
	episodeID := finishDraft(t, fx, owner)
	if code := postDone(t, routes, cookie, episodeID); code != http.StatusAccepted {
		t.Fatalf("mark done status = %d, want 202", code)
	}
	waitState(t, fx, episodeID, episode.StateReady)
	var fallback int
	if err := fx.db.Reader().QueryRowContext(t.Context(),
		`SELECT fallback FROM covers WHERE episode_id = ?`, episodeID).Scan(&fallback); err != nil {
		t.Fatalf("read cover: %v", err)
	}
	if fallback != 1 {
		t.Fatalf("cover fallback = %d, want the plain cover", fallback)
	}

	other := finishDraft(t, fx, owner)
	if _, err := fx.db.Writer().ExecContext(t.Context(),
		`UPDATE episodes SET state = ? WHERE id = ?`, string(episode.StateAnalysing), other); err != nil {
		t.Fatalf("plant analysing: %v", err)
	}
	j.settlePass(t.Context(), kindCover, episodeDescriptor{OwnerID: owner, EpisodeID: other, RenderID: "render-none"},
		errors.New("scripted budget refusal"))
	if n := countRows(t, fx, `SELECT COUNT(*) FROM covers WHERE episode_id = ? AND fallback = 1`, other); n != 1 {
		t.Fatalf("covers = %d, want the plain cover after a failed pass", n)
	}
}

// TestRestartInterruptsPaidPassesAndShips leaves an analysis pass and a
// cover pass running on an analysing episode with a render, then boots
// the job wiring the way a restart does. Neither pass reruns. The cover
// falls back to the plain panel, no marking starts after the lost
// analysis, and the episode ships ready.
func TestRestartInterruptsPaidPassesAndShips(t *testing.T) {
	fx := openWireFixture(t)
	useFinishModels(fx, false)
	const owner = "owner-restart-finish"
	insertWireUser(t, fx, owner)
	episodeID := draftEpisode(t, fx, owner)
	if _, err := fx.db.Writer().ExecContext(t.Context(),
		`INSERT INTO renders (id, owner_id, episode_id, input_hash, opus_media_id, aac_media_id, loudness)
		 VALUES ('render-live', ?, ?, 'hash-live', 'opus-live', 'aac-live', -16)`, owner, episodeID); err != nil {
		t.Fatalf("seed render: %v", err)
	}
	if _, err := fx.db.Writer().ExecContext(t.Context(),
		`UPDATE episodes SET state = ? WHERE id = ?`, string(episode.StateAnalysing), episodeID); err != nil {
		t.Fatalf("plant analysing: %v", err)
	}
	desc := episodeDescriptor{OwnerID: owner, EpisodeID: episodeID, RenderID: "render-live"}
	plantRunningJob(t, fx, "job-lost-analysis", kindAnalysis, desc)
	plantRunningJob(t, fx, "job-lost-cover", kindCover, desc)

	j := bootFinishJobs(t, fx)
	waitIdle(t, fx)
	for _, id := range []string{"job-lost-analysis", "job-lost-cover"} {
		rec, err := j.store.Get(t.Context(), id)
		if err != nil {
			t.Fatalf("read %s: %v", id, err)
		}
		if rec.Status != job.StatusInterrupted {
			t.Fatalf("%s status = %s, want interrupted", id, rec.Status)
		}
	}
	for _, kind := range []string{kindAnalysis, kindCover} {
		if n := kindJobsFor(t, fx, kind, episodeID); n != 1 {
			t.Fatalf("%s jobs = %d, want the interrupted one alone", kind, n)
		}
	}
	if n := kindJobsFor(t, fx, kindMemory, episodeID); n != 0 {
		t.Fatalf("marking jobs = %d, want none after a lost analysis", n)
	}
	if n := countRows(t, fx, `SELECT COUNT(*) FROM covers WHERE episode_id = ? AND fallback = 1`, episodeID); n != 1 {
		t.Fatalf("covers = %d, want the plain cover", n)
	}
	if got := finishState(t, fx, episodeID); got != episode.StateReady {
		t.Fatalf("state = %s, want ready", got)
	}
}

// TestRestartKeepsDraftAfterLostEditorial leaves an editorial pass
// running on a draft, then boots the job wiring. The pass never reruns,
// and the draft stays a draft, so mark done still ships it with its plain
// title.
func TestRestartKeepsDraftAfterLostEditorial(t *testing.T) {
	fx := openWireFixture(t)
	const owner = "owner-restart-editorial"
	insertWireUser(t, fx, owner)
	episodeID := draftEpisode(t, fx, owner)
	seedEditWords(t, fx, owner, episodeID)
	if _, err := fx.db.Writer().ExecContext(t.Context(),
		`UPDATE episodes SET state = ? WHERE id = ?`, string(episode.StateDraft), episodeID); err != nil {
		t.Fatalf("plant draft: %v", err)
	}
	plantRunningJob(t, fx, "job-lost-editorial", kindEditorial, episodeDescriptor{OwnerID: owner, EpisodeID: episodeID})
	j := bootFinishJobs(t, fx)
	rec, err := j.store.Get(t.Context(), "job-lost-editorial")
	if err != nil {
		t.Fatalf("read job: %v", err)
	}
	if rec.Status != job.StatusInterrupted {
		t.Fatalf("editorial status = %s, want interrupted", rec.Status)
	}
	if n := kindJobsFor(t, fx, kindEditorial, episodeID); n != 1 {
		t.Fatalf("editorial jobs = %d, want the interrupted one alone", n)
	}
	if got := finishState(t, fx, episodeID); got != episode.StateDraft {
		t.Fatalf("state = %s, want draft", got)
	}
}

// TestRestartResumesRenderIntoTheChain leaves a render running, then
// boots the job wiring. The render calls nothing paid, so it resumes, and
// the resumed attempt still moves the episode through analysis to ready.
func TestRestartResumesRenderIntoTheChain(t *testing.T) {
	fx := openWireFixture(t)
	useFinishModels(fx, false)
	const owner = "owner-restart-render"
	insertWireUser(t, fx, owner)
	episodeID := finishDraft(t, fx, owner)
	if _, err := fx.db.Writer().ExecContext(t.Context(),
		`UPDATE episodes SET state = ? WHERE id = ?`, string(episode.StateRendering), episodeID); err != nil {
		t.Fatalf("plant rendering: %v", err)
	}
	plantRunningJob(t, fx, "job-lost-render", kindRender, episodeDescriptor{OwnerID: owner, EpisodeID: episodeID})
	bootFinishJobs(t, fx)
	waitState(t, fx, episodeID, episode.StateReady)
	if n := kindJobsFor(t, fx, kindRender, episodeID); n != 2 {
		t.Fatalf("render attempts = %d, want the interrupted one and its resume", n)
	}
	if n := kindJobsFor(t, fx, kindAnalysis, episodeID); n != 1 {
		t.Fatalf("analysis jobs = %d, want one", n)
	}
}

// TestRenderStampsItsEpisode starts a render through mark done and reads
// its stamp before the work reports anything. The detail can name the
// render pass from its first read.
func TestRenderStampsItsEpisode(t *testing.T) {
	fx := openWireFixture(t)
	useFinishModels(fx, false)
	j := bootFinishJobs(t, fx)
	svc, err := episode.NewService(episodeConfig(fx.db, j))
	if err != nil {
		t.Fatalf("new episode service: %v", err)
	}
	const owner = "owner-render-stamp"
	insertWireUser(t, fx, owner)
	episodeID := finishDraft(t, fx, owner)
	jobID, err := svc.RequestRender(t.Context(), owner, episodeID)
	if err != nil {
		t.Fatalf("request render: %v", err)
	}
	got, err := svc.RenderOutcome(t.Context(), owner, episodeID)
	if err != nil {
		t.Fatalf("render outcome: %v", err)
	}
	if !got.Found || got.JobID != jobID {
		t.Fatalf("render outcome = %+v, want job %s", got, jobID)
	}
	waitState(t, fx, episodeID, episode.StateReady)
}

// TestDuplicateKindRefusesBoot boots the job wiring with a feature kind
// that reuses a core kind name, and then with two features that clash.
// Each boot refuses before the runner opens.
func TestDuplicateKindRefusesBoot(t *testing.T) {
	fx := openWireFixture(t)
	_, err := tryBootFinishJobs(t, fx, map[string]job.Kind{kindRender: {Limit: 4}})
	if !errors.Is(err, errDuplicateKind) {
		t.Fatalf("core clash boot error = %v, want errDuplicateKind", err)
	}
	_, err = tryBootFinishJobs(t, fx,
		map[string]job.Kind{"erase": {Limit: 1}},
		map[string]job.Kind{"erase": {Limit: 2}})
	if !errors.Is(err, errDuplicateKind) {
		t.Fatalf("feature clash boot error = %v, want errDuplicateKind", err)
	}
	if !strings.Contains(err.Error(), "erase") {
		t.Fatalf("boot error = %v, want the clashing name", err)
	}
	clash := []feature{
		{name: "one", kinds: func(kindWiring) (map[string]job.Kind, error) {
			return map[string]job.Kind{kindAnalysis: {}}, nil
		}},
	}
	extra, err := featureKinds(clash, kindWiring{DB: fx.db})
	if err != nil {
		t.Fatalf("feature kinds: %v", err)
	}
	if _, err := tryBootFinishJobs(t, fx, extra...); !errors.Is(err, errDuplicateKind) {
		t.Fatalf("hook clash boot error = %v, want errDuplicateKind", err)
	}
	j := bootFinishJobs(t, fx, map[string]job.Kind{"erase": {Limit: 1}})
	if j.jobRunner() == nil {
		t.Fatal("a distinct feature kind refused the boot")
	}
}

// TestFeatureHooksAreWired requires every feature file to register its
// two hooks with the boot, and the hooks to register nothing yet.
func TestFeatureHooksAreWired(t *testing.T) {
	feats := features()
	names := map[string]bool{}
	for _, f := range feats {
		if f.kinds == nil || f.mount == nil {
			t.Fatalf("feature %s misses a hook", f.name)
		}
		names[f.name] = true
	}
	for _, want := range []string{"privacy", "seed", "export"} {
		if !names[want] {
			t.Fatalf("features = %v, want %s", names, want)
		}
	}
	extra, err := featureKinds(feats, kindWiring{})
	if err != nil {
		t.Fatalf("feature kinds: %v", err)
	}
	if _, err := mergeKinds(map[string]job.Kind{kindRender: {}}, extra...); err != nil {
		t.Fatalf("merge feature kinds: %v", err)
	}
	mux := http.NewServeMux()
	if err := mountFeatures(t.Context(), feats, routeWiring{Mux: mux}); err != nil {
		t.Fatalf("mount features: %v", err)
	}
	refused := []feature{{name: "broken", mount: func(context.Context, routeWiring) error {
		return errors.New("scripted catalog refusal")
	}}}
	if err := mountFeatures(t.Context(), refused, routeWiring{Mux: mux}); err == nil || !strings.Contains(err.Error(), "broken") {
		t.Fatalf("mount error = %v, want the refusal named by feature", err)
	}
}

// TestEditorialKindHasOneSource pins the editorial, analysis and marking
// kinds the binary starts to the names the episode detail reads back.
func TestEditorialKindHasOneSource(t *testing.T) {
	if kindEditorial != episode.EditorialKind || kindAnalysis != episode.AnalysisKind || kindMemory != episode.MemoryKind {
		t.Fatal("the binary names a pass kind the detail does not read")
	}
}

// keepSessions answers the provider session calls retention may make.
type keepSessions struct{}

// TerminateSession ends nothing, because no provider session is open.
func (keepSessions) TerminateSession(context.Context, string) (assemblyai.TerminateResult, error) {
	return assemblyai.TerminateResult{}, nil
}

// keepTranscripts answers the provider transcript calls retention may
// make.
type keepTranscripts struct{}

// Delete removes nothing, because no provider transcript exists.
func (keepTranscripts) Delete(context.Context, string) error { return nil }

// Get returns an empty transcript.
func (keepTranscripts) Get(context.Context, string) (assemblyai.Transcript, error) {
	return assemblyai.Transcript{}, nil
}

// TestKeptEpisodeLeavesTheGuestDeletable stores rendered words for a
// guest episode, keeps the episode for another user, and then deletes
// the guest row the way the retention sweep does. The render link
// follows the episode, so the kept words still name their render and
// nothing still points at the guest.
func TestKeptEpisodeLeavesTheGuestDeletable(t *testing.T) {
	fx := openWireFixture(t)
	const guest = "guest-kept-link"
	const keeper = "keeper-kept-link"
	insertWireUser(t, fx, guest)
	insertWireUser(t, fx, keeper)
	episodeID := draftEpisode(t, fx, guest)
	if _, err := fx.db.Writer().ExecContext(t.Context(),
		`INSERT INTO renders (id, owner_id, episode_id, input_hash, opus_media_id, aac_media_id, loudness)
		 VALUES ('render-kept', ?, ?, 'hash-kept', 'opus-kept', 'aac-kept', -16)`, guest, episodeID); err != nil {
		t.Fatalf("seed render: %v", err)
	}
	if err := analysis.ReplaceWords(t.Context(), fx.db.Writer(), guest, episodeID, "render-kept",
		[]analysis.Word{{Text: "hello", StartMs: 0, EndMs: 100}}); err != nil {
		t.Fatalf("replace words: %v", err)
	}
	svc, err := retention.New(retention.Config{
		DB: fx.db, Media: fx.media, CoverDir: t.TempDir(),
		Sessions: keepSessions{}, Transcripts: keepTranscripts{}, Window: time.Hour,
	})
	if err != nil {
		t.Fatalf("open retention: %v", err)
	}
	if err := svc.Keep(t.Context(), episodeID, keeper); err != nil {
		t.Fatalf("keep: %v", err)
	}
	source, err := analysis.RenderedSource(t.Context(), fx.db.Reader(), episodeID)
	if err != nil {
		t.Fatalf("rendered source: %v", err)
	}
	if source != "render-kept" {
		t.Fatalf("rendered source = %q after keep, want render-kept", source)
	}
	if _, err := fx.db.Writer().ExecContext(t.Context(),
		`DELETE FROM users WHERE id = ? AND kind = 'guest'`, guest); err != nil {
		t.Fatalf("delete the guest after keep: %v", err)
	}
}

// TestEveryStuckRenderShipsOneAtATime leaves three episodes rendering
// with no render job behind them, the state a restart leaves. The render
// kind runs one job at a time, so the boot starts one render and the
// others wait. Each advance then starts the next waiting render once the
// slot frees. Every episode ships with exactly one render, and a repeat
// advance starts nothing more.
func TestEveryStuckRenderShipsOneAtATime(t *testing.T) {
	fx := openWireFixture(t)
	useFinishModels(fx, false)
	const owner = "owner-stuck-renders"
	insertWireUser(t, fx, owner)
	var stuck []string
	for range 3 {
		episodeID := finishDraft(t, fx, owner)
		if _, err := fx.db.Writer().ExecContext(t.Context(),
			`UPDATE episodes SET state = ? WHERE id = ?`, string(episode.StateRendering), episodeID); err != nil {
			t.Fatalf("plant rendering: %v", err)
		}
		stuck = append(stuck, episodeID)
	}
	j := bootFinishJobs(t, fx)
	deadline := time.Now().Add(2 * time.Minute)
	for {
		shipped := 0
		for _, episodeID := range stuck {
			if finishState(t, fx, episodeID) == episode.StateReady {
				shipped++
			}
		}
		if shipped == len(stuck) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d of %d stuck episodes shipped", shipped, len(stuck))
		}
		j.advanceAll(t.Context())
		time.Sleep(10 * time.Millisecond)
	}
	waitIdle(t, fx)
	j.advanceAll(t.Context())
	waitIdle(t, fx)
	for _, episodeID := range stuck {
		if n := kindJobsFor(t, fx, kindRender, episodeID); n != 1 {
			t.Fatalf("render jobs for %s = %d, want one", episodeID, n)
		}
		if n := kindJobsFor(t, fx, kindAnalysis, episodeID); n != 1 {
			t.Fatalf("analysis jobs for %s = %d, want one", episodeID, n)
		}
	}
}

// failingStampStore refuses the scheduler's first linkage write while the
// runner keeps its real store. It models a job record that starts but cannot
// be connected to its episode.
type failingStampStore struct {
	job.Store
	err error
}

// SetProgress refuses the scheduler's linkage write.
func (s failingStampStore) SetProgress(context.Context, job.Record) error {
	return s.err
}

// TestRetryFailedRenderStartReturnsToFailed retries an initial render start
// fault through the done route. A later linkage fault must fail the episode,
// rather than leave an unbounded rendering row with no linked render.
func TestRetryFailedRenderStartReturnsToFailed(t *testing.T) {
	fx := openWireFixture(t)
	useFinishModels(fx, false)
	j := bootFinishJobs(t, fx)
	routes, guests := finishRoutes(t, fx, j)
	cookie, owner := finishGuest(t, guests)
	episodeID := finishDraft(t, fx, owner)

	runner := j.runner
	j.runner = nil
	if got := postDone(t, routes, cookie, episodeID); got != http.StatusServiceUnavailable {
		t.Fatalf("initial done status = %d, want 503", got)
	}
	j.runner = runner
	if got := finishState(t, fx, episodeID); got != episode.StateFailed {
		t.Fatalf("initial state = %s, want failed", got)
	}

	j.store = failingStampStore{Store: j.store, err: errors.New("linkage write refused")}
	if got := postDone(t, routes, cookie, episodeID); got != http.StatusAccepted {
		t.Fatalf("retry done status = %d, want 202", got)
	}
	j.advanceAll(t.Context())
	if got := finishState(t, fx, episodeID); got != episode.StateFailed {
		t.Fatalf("state after failed queued start = %s, want failed", got)
	}
}

// TestMarkDoneJoinsTheWaitingRender moves a draft to rendering the way
// mark done does, lets the advance start its render first, and then
// starts the render the way mark done does. The second start answers with
// the running render, so the episode carries one render.
func TestMarkDoneJoinsTheWaitingRender(t *testing.T) {
	fx := openWireFixture(t)
	useFinishModels(fx, false)
	j := bootFinishJobs(t, fx)
	const owner = "owner-render-join"
	insertWireUser(t, fx, owner)
	episodeID := finishDraft(t, fx, owner)
	if err := episode.MarkDone(t.Context(), fx.db, episodeID); err != nil {
		t.Fatalf("mark done: %v", err)
	}
	if err := j.startWaitingRender(t.Context(), owner, episodeID); err != nil {
		t.Fatalf("start waiting render: %v", err)
	}
	first, err := episode.LastKindJob(t.Context(), fx.db, episodeID, kindRender)
	if err != nil || !first.Found {
		t.Fatalf("waiting render = %+v, %v, want one", first, err)
	}
	jobID, err := j.StartEpisodeKind(t.Context(), kindRender, owner, episodeID, j.renderFunc(owner, episodeID))
	if err != nil {
		t.Fatalf("mark done start: %v", err)
	}
	if jobID != first.JobID {
		t.Fatalf("mark done started %s, want the running render %s", jobID, first.JobID)
	}
	waitState(t, fx, episodeID, episode.StateReady)
	if n := kindJobsFor(t, fx, kindRender, episodeID); n != 1 {
		t.Fatalf("render jobs = %d, want one", n)
	}
}

// TestMarkDoneWhileRenderBusyWaitsAndShips holds the one render slot with
// guest A's render, the way a second guest's render or a render the boot
// started does. Guest B marks done while it runs. B answers 202 with its
// render queued and waits in rendering, never failed. A repeat tap starts
// nothing. Once the slot frees, the advance renders B, and both episodes
// ship with one render each.
func TestMarkDoneWhileRenderBusyWaitsAndShips(t *testing.T) {
	fx := openWireFixture(t)
	useFinishModels(fx, false)
	j := bootFinishJobs(t, fx)
	routes, guests := finishRoutes(t, fx, j)
	_, ownerA := finishGuest(t, guests)
	cookieB, ownerB := finishGuest(t, guests)
	first := finishDraft(t, fx, ownerA)
	second := finishDraft(t, fx, ownerB)

	release := make(chan struct{})
	var once sync.Once
	free := func() { once.Do(func() { close(release) }) }
	t.Cleanup(free)
	if err := episode.MarkDone(t.Context(), fx.db, first); err != nil {
		t.Fatalf("mark first done: %v", err)
	}
	held := func(ctx context.Context, progress func(job.Progress)) ([]byte, error) {
		<-release
		return j.renderFunc(ownerA, first)(ctx, progress)
	}
	if _, err := j.StartEpisodeKind(t.Context(), kindRender, ownerA, first, held); err != nil {
		t.Fatalf("hold the render slot: %v", err)
	}

	code, body := postDoneBody(t, routes, cookieB, second)
	if got := finishState(t, fx, second); got != episode.StateRendering {
		t.Fatalf("mark done on a busy render slot answered %d and left the episode %s, want rendering", code, got)
	}
	if code != http.StatusAccepted {
		t.Fatalf("mark done on a busy render slot answered %d, want %d", code, http.StatusAccepted)
	}
	var queued struct {
		JobID  string `json:"job_id"`
		Queued bool   `json:"queued"`
		State  string `json:"state"`
	}
	if err := json.Unmarshal(body, &queued); err != nil {
		t.Fatalf("decode done: %v", err)
	}
	if !queued.Queued || queued.JobID != "" || queued.State != string(episode.StateRendering) {
		t.Fatalf("done = %+v, want a queued render with no job", queued)
	}
	if code := postDone(t, routes, cookieB, second); code != http.StatusConflict {
		t.Fatalf("repeat mark done answered %d, want %d", code, http.StatusConflict)
	}
	j.advanceAll(t.Context())
	if n := kindJobsFor(t, fx, kindRender, second); n != 0 {
		t.Fatalf("render jobs for the waiting episode = %d while the slot is held, want none", n)
	}

	free()
	deadline := time.Now().Add(2 * time.Minute)
	for {
		j.advanceAll(t.Context())
		if finishState(t, fx, first) == episode.StateReady && finishState(t, fx, second) == episode.StateReady {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("first %s, second %s, want both ready", finishState(t, fx, first), finishState(t, fx, second))
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, id := range []string{first, second} {
		if n := kindJobsFor(t, fx, kindRender, id); n != 1 {
			t.Fatalf("render jobs for %s = %d, want one", id, n)
		}
	}
}

// TestRenderStartJoinsTheRenderThatShipped starts a render the way mark
// done does for an episode a finished render already moved past
// rendering. The start answers with that render and starts no second one.
func TestRenderStartJoinsTheRenderThatShipped(t *testing.T) {
	fx := openWireFixture(t)
	useFinishModels(fx, false)
	j := bootFinishJobs(t, fx)
	const owner = "owner-render-shipped"
	insertWireUser(t, fx, owner)
	episodeID := finishDraft(t, fx, owner)
	if err := episode.MarkDone(t.Context(), fx.db, episodeID); err != nil {
		t.Fatalf("mark done: %v", err)
	}
	if err := j.startWaitingRender(t.Context(), owner, episodeID); err != nil {
		t.Fatalf("start waiting render: %v", err)
	}
	waitState(t, fx, episodeID, episode.StateReady)
	first, err := episode.LastKindJob(t.Context(), fx.db, episodeID, kindRender)
	if err != nil || !first.Found {
		t.Fatalf("finished render = %+v, %v, want one", first, err)
	}
	jobID, err := j.StartEpisodeKind(t.Context(), kindRender, owner, episodeID, j.renderFunc(owner, episodeID))
	if err != nil {
		t.Fatalf("late render start: %v", err)
	}
	if jobID != first.JobID {
		t.Fatalf("late render start answered %s, want the finished render %s", jobID, first.JobID)
	}
	if n := kindJobsFor(t, fx, kindRender, episodeID); n != 1 {
		t.Fatalf("render jobs = %d, want one", n)
	}
}

// TestSettledOutcomesDrainOnShip ships one episode through the chain and
// then advances again. The settled outcomes the passes left serve only an
// analysing episode, so none stays once the episode is ready.
func TestSettledOutcomesDrainOnShip(t *testing.T) {
	fx := openWireFixture(t)
	useFinishModels(fx, false)
	j := bootFinishJobs(t, fx)
	const owner = "owner-settled-drain"
	insertWireUser(t, fx, owner)
	episodeID := finishDraft(t, fx, owner)
	svc, err := episode.NewService(episodeConfig(fx.db, j))
	if err != nil {
		t.Fatalf("new episode service: %v", err)
	}
	if _, err := svc.RequestRender(t.Context(), owner, episodeID); err != nil {
		t.Fatalf("request render: %v", err)
	}
	waitState(t, fx, episodeID, episode.StateReady)
	j.advanceAll(t.Context())
	j.schedMu.Lock()
	held := len(j.settled)
	j.schedMu.Unlock()
	if held != 0 {
		t.Fatalf("settled holds %d outcomes after the episode shipped, want none", held)
	}
}
