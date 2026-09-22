package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nrynss/keel/job"
	"github.com/nrynss/keel/sqlite"
	"github.com/nrynss/reprise/internal/episode"
	"github.com/nrynss/reprise/internal/identity"
	"github.com/nrynss/reprise/internal/memory"
	"github.com/nrynss/reprise/internal/store"
)

// errBrokenStart is the runner fault the unavailable test binds.
var errBrokenStart = errors.New("dial runner: refused")

// openDiary opens a migrated database with the guest sessions behind it,
// and returns both.
func openDiary(t *testing.T) (*sqlite.DB, *identity.Service) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "diary.db")
	db, err := sqlite.Open(t.Context(), sqlite.Config{
		Path:   path,
		Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := store.Open(t.Context(), db); err != nil {
		t.Fatalf("open diary store: %v", err)
	}
	if err := memory.Migrate(t.Context(), db); err != nil {
		t.Fatalf("open memory index: %v", err)
	}
	svc, err := identity.New(t.Context(), identity.Config{
		DB:         db,
		SigningKey: "test-signing-key-with-enough-length",
	})
	if err != nil {
		t.Fatalf("open guest sessions: %v", err)
	}
	return db, svc
}

// mintGuest runs one anonymous request through the middleware and returns
// the minted cookie with its user.
func mintGuest(t *testing.T, svc *identity.Service) (*http.Cookie, identity.User) {
	t.Helper()
	var user identity.User
	wrapped := svc.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		var ok bool
		user, ok = identity.UserFromContext(r.Context())
		if !ok {
			t.Errorf("middleware set no user")
		}
	}))
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	for _, c := range rec.Result().Cookies() {
		if c.Name == identity.CookieName {
			return c, user
		}
	}
	t.Fatalf("mint set no session cookie")
	return nil, identity.User{}
}

// serve runs one request through the guest middleware into the handler,
// carrying the cookie when one is given.
func serve(svc *identity.Service, handler http.Handler, cookie *http.Cookie, req *http.Request) *httptest.ResponseRecorder {
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	svc.Middleware(handler).ServeHTTP(rec, req)
	return rec
}

// seedEpisodeRow writes one episode row and fails the test on error.
func seedEpisodeRow(t *testing.T, db *sqlite.DB, episodeID, owner string, number int64, state string) {
	t.Helper()
	if _, err := db.Writer().ExecContext(t.Context(), `INSERT INTO episodes
		(id, owner_id, number, title, state, visibility, share_token, seeded)
		VALUES (?, ?, ?, ?, ?, 'private', 'share', 0)`,
		episodeID, owner, number, "Title "+episodeID, state); err != nil {
		t.Fatalf("seed episode %s: %v", episodeID, err)
	}
}

// seedSessionRow writes one diary session row and fails the test on error.
func seedSessionRow(t *testing.T, db *sqlite.DB, sessionID, owner, episodeID string) {
	t.Helper()
	if _, err := db.Writer().ExecContext(t.Context(),
		`INSERT INTO sessions (id, owner_id, episode_id, provider_session_id, token_cap, connected_seconds)
		 VALUES (?, ?, ?, '', 1800, 0)`, sessionID, owner, episodeID); err != nil {
		t.Fatalf("seed session %s: %v", sessionID, err)
	}
}

// seedProposalRow writes one cut proposal and fails the test on error.
func seedProposalRow(t *testing.T, db *sqlite.DB, proposalID, owner, episodeID string) {
	t.Helper()
	if _, err := db.Writer().ExecContext(t.Context(),
		`INSERT INTO proposals (id, owner_id, episode_id, kind, start_word, end_word, reason)
		 VALUES (?, ?, ?, 'cut', 0, 1, 'test')`, proposalID, owner, episodeID); err != nil {
		t.Fatalf("seed proposal %s: %v", proposalID, err)
	}
}

// seedMentionRow writes one mention row and fails the test on error.
func seedMentionRow(t *testing.T, db *sqlite.DB, mentionID, owner, episodeID, kind, quote string, offset int) {
	t.Helper()
	if _, err := db.Writer().ExecContext(t.Context(),
		`INSERT INTO mentions (id, owner_id, episode_id, kind, word_offset, quote)
		 VALUES (?, ?, ?, ?, ?, ?)`, mentionID, owner, episodeID, kind, offset, quote); err != nil {
		t.Fatalf("seed mention %s: %v", mentionID, err)
	}
}

// episodeService wires the real episode service with a counting starter,
// so handler tests pin the started job without opening a runner.
type episodeService struct {
	svc     *episode.Service
	starter *countingStarter
}

// countingStarter records every start and answers a fixed id or error.
type countingStarter struct {
	calls int
	kinds []string
	id    string
	err   error
}

// StartKind records the call and answers the fixed outcome.
func (f *countingStarter) StartKind(_ context.Context, kind string, _ job.Func) (string, error) {
	f.calls++
	f.kinds = append(f.kinds, kind)
	if f.err != nil {
		return "", f.err
	}
	return f.id, nil
}

// stubRender builds work the starter never runs.
type stubRender struct{}

// Func returns a no-op job func.
func (stubRender) Func(_, _ string) job.Func {
	return func(_ context.Context, _ func(job.Progress)) ([]byte, error) {
		return []byte("{}"), nil
	}
}

// newEpisodeService returns the real service with a counting starter.
func newEpisodeService(t *testing.T, db *sqlite.DB, err error) *episodeService {
	t.Helper()
	starter := &countingStarter{id: "job-1", err: err}
	svc, svcErr := episode.NewService(episode.Config{
		DB:         db,
		Starter:    starter,
		RenderKind: "render",
		Render:     stubRender{},
	})
	if svcErr != nil {
		t.Fatalf("new episode service: %v", svcErr)
	}
	return &episodeService{svc: svc, starter: starter}
}

// envelopeCode reads the error code out of a refusal body.
func envelopeCode(t *testing.T, rec *httptest.ResponseRecorder) (int, string) {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode refusal: %v", err)
	}
	return rec.Code, body.Error.Code
}

// episodeListBody decodes a list body.
func episodeListBody(t *testing.T, rec *httptest.ResponseRecorder) episodeListJSON {
	t.Helper()
	var body episodeListJSON
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode episode list: %v", err)
	}
	return body
}

// TestEpisodeListServesOwnerOnly requires each guest to list only its own
// episodes, and a request with no middleware to answer 401.
func TestEpisodeListServesOwnerOnly(t *testing.T) {
	t.Parallel()
	db, guests := openDiary(t)
	ownerCookie, owner := mintGuest(t, guests)
	strangerCookie, _ := mintGuest(t, guests)
	seedEpisodeRow(t, db, "ep-1", owner.ID, 1, "draft")
	seedEpisodeRow(t, db, "ep-2", owner.ID, 2, "ready")
	handler := NewEpisodes(newEpisodeService(t, db, nil).svc)

	rec := serve(guests, handler, ownerCookie, httptest.NewRequest(http.MethodGet, "/api/episodes", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200", rec.Code)
	}
	if got := episodeListBody(t, rec); len(got.Episodes) != 2 || got.Episodes[0].ID != "ep-1" {
		t.Fatalf("list = %+v, want ep-1 then ep-2", got)
	}
	if cache := rec.Header().Get("Cache-Control"); cache != "no-store" {
		t.Fatalf("cache header = %q, want no-store", cache)
	}

	rec = serve(guests, handler, strangerCookie, httptest.NewRequest(http.MethodGet, "/api/episodes", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("stranger list status = %d, want 200", rec.Code)
	}
	if got := episodeListBody(t, rec); len(got.Episodes) != 0 {
		t.Fatalf("stranger list = %+v, want no episodes", got)
	}

	direct := httptest.NewRecorder()
	handler.ServeHTTP(direct, httptest.NewRequest(http.MethodGet, "/api/episodes", nil))
	if status, code := envelopeCode(t, direct); status != http.StatusUnauthorized || code != CodeSessionRequired {
		t.Fatalf("no middleware status = %d code = %q, want 401 session_required", status, code)
	}
}

// TestEpisodeDetailReadsBackDecisions accepts then reverts one proposal
// through the handler and reads each decision back on the detail.
func TestEpisodeDetailReadsBackDecisions(t *testing.T) {
	t.Parallel()
	db, guests := openDiary(t)
	cookie, owner := mintGuest(t, guests)
	seedEpisodeRow(t, db, "ep-1", owner.ID, 1, "draft")
	seedProposalRow(t, db, "cut-1", owner.ID, "ep-1")
	handler := NewEpisodes(newEpisodeService(t, db, nil).svc)
	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/episodes/ep-1/decisions", strings.NewReader(body))
		return serve(guests, handler, cookie, req)
	}
	decide := func(decision string) {
		t.Helper()
		rec := post(`{"proposal_id":"cut-1","decision":"` + decision + `"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("decide %s status = %d, want 200", decision, rec.Code)
		}
	}
	detail := func() episodeDetailJSON {
		t.Helper()
		rec := serve(guests, handler, cookie, httptest.NewRequest(http.MethodGet, "/api/episodes/ep-1", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("detail status = %d, want 200", rec.Code)
		}
		var body episodeDetailJSON
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("decode detail: %v", err)
		}
		return body
	}

	decide("accepted")
	if got := detail(); len(got.Proposals) != 1 || got.Proposals[0].Decision != "accepted" {
		t.Fatalf("detail after accept = %+v, want one accepted cut", got)
	}
	decide("reverted")
	if got := detail(); len(got.Proposals) != 1 || got.Proposals[0].Decision != "reverted" {
		t.Fatalf("detail after revert = %+v, want one reverted cut", got)
	}

	for _, tc := range []struct {
		name   string
		body   string
		status int
		code   string
	}{
		{"malformed", "{", http.StatusBadRequest, CodeInvalidRequest},
		{"empty proposal", `{"decision":"accepted"}`, http.StatusBadRequest, CodeInvalidRequest},
		{"bad value", `{"proposal_id":"cut-1","decision":"maybe"}`, http.StatusBadRequest, CodeInvalidRequest},
		{"unknown proposal", `{"proposal_id":"missing","decision":"accepted"}`, http.StatusNotFound, CodeProposalNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := post(tc.body)
			if status, code := envelopeCode(t, rec); status != tc.status || code != tc.code {
				t.Fatalf("status = %d code = %q, want %d %q", status, code, tc.status, tc.code)
			}
		})
	}

	foreign, _ := mintGuest(t, guests)
	rec := serve(guests, handler, foreign, httptest.NewRequest(http.MethodGet, "/api/episodes/ep-1", nil))
	if status, code := envelopeCode(t, rec); status != http.StatusNotFound || code != CodeEpisodeNotFound {
		t.Fatalf("foreign detail status = %d code = %q, want 404 episode_not_found", status, code)
	}
}

// TestMarkDoneStartsRenderOnce marks a draft done through the handler and
// requires one render start, with a repeat tap refused and a stranger
// answered 404.
func TestMarkDoneStartsRenderOnce(t *testing.T) {
	t.Parallel()
	db, guests := openDiary(t)
	cookie, owner := mintGuest(t, guests)
	seedEpisodeRow(t, db, "ep-1", owner.ID, 1, "draft")
	seedEpisodeRow(t, db, "ep-2", owner.ID, 2, "recording")
	stack := newEpisodeService(t, db, nil)
	handler := NewEpisodes(stack.svc)
	done := func(cookie *http.Cookie, episodeID string) *httptest.ResponseRecorder {
		return serve(guests, handler, cookie,
			httptest.NewRequest(http.MethodPost, "/api/episodes/"+episodeID+"/done", nil))
	}

	rec := done(cookie, "ep-1")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("done status = %d, want 202", rec.Code)
	}
	var body doneJSON
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode done: %v", err)
	}
	if body.JobID != "job-1" || body.EpisodeID != "ep-1" || body.State != "rendering" {
		t.Fatalf("done = %+v, want job-1 on ep-1 rendering", body)
	}
	if stack.starter.calls != 1 || len(stack.starter.kinds) != 1 || stack.starter.kinds[0] != "render" {
		t.Fatalf("starter calls = %d, want one render start", stack.starter.calls)
	}

	rec = done(cookie, "ep-1")
	if status, code := envelopeCode(t, rec); status != http.StatusConflict || code != CodeIllegalTransition {
		t.Fatalf("repeat done status = %d code = %q, want 409 illegal_transition", status, code)
	}
	if stack.starter.calls != 1 {
		t.Fatalf("starter calls = %d, want no second start", stack.starter.calls)
	}

	rec = done(cookie, "ep-2")
	if status, code := envelopeCode(t, rec); status != http.StatusConflict || code != CodeIllegalTransition {
		t.Fatalf("recording done status = %d code = %q, want 409 illegal_transition", status, code)
	}

	foreign, _ := mintGuest(t, guests)
	rec = done(foreign, "ep-1")
	if status, code := envelopeCode(t, rec); status != http.StatusNotFound || code != CodeEpisodeNotFound {
		t.Fatalf("foreign done status = %d code = %q, want 404 episode_not_found", status, code)
	}
}

// TestMarkDoneReportsStarterFaults requires a full queue to answer 429
// and a broken start to answer 503, with the episode waiting in failed.
func TestMarkDoneReportsStarterFaults(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"full queue", job.ErrLimit, http.StatusTooManyRequests, CodeRenderBusy},
		{"broken start", errBrokenStart, http.StatusServiceUnavailable, CodeRenderUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			db, guests := openDiary(t)
			cookie, owner := mintGuest(t, guests)
			seedEpisodeRow(t, db, "ep-1", owner.ID, 1, "draft")
			stack := newEpisodeService(t, db, tc.err)
			handler := NewEpisodes(stack.svc)
			rec := serve(guests, handler, cookie,
				httptest.NewRequest(http.MethodPost, "/api/episodes/ep-1/done", nil))
			if status, code := envelopeCode(t, rec); status != tc.status || code != tc.code {
				t.Fatalf("status = %d code = %q, want %d %q", status, code, tc.status, tc.code)
			}
			var state string
			if err := db.Reader().QueryRowContext(t.Context(),
				"SELECT state FROM episodes WHERE id = 'ep-1'").Scan(&state); err != nil {
				t.Fatalf("read state: %v", err)
			}
			if state != "failed" {
				t.Fatalf("state = %q, want failed", state)
			}
		})
	}
}

// TestSessionEndRecordsProviderClose ends a diary session through the
// handler and requires the provider id to land on the row.
func TestSessionEndRecordsProviderClose(t *testing.T) {
	t.Parallel()
	db, guests := openDiary(t)
	cookie, owner := mintGuest(t, guests)
	seedEpisodeRow(t, db, "ep-1", owner.ID, 1, "recording")
	seedSessionRow(t, db, "sess-1", owner.ID, "ep-1")
	stack := newEpisodeService(t, db, nil)
	handler := NewSessionEnd(stack.svc)
	end := func(cookie *http.Cookie, sessionID, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+sessionID+"/end", strings.NewReader(body))
		return serve(guests, handler, cookie, req)
	}

	rec := end(cookie, "sess-1", `{"provider_session_id":"prov-9"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("end status = %d, want 200", rec.Code)
	}
	var body sessionEndJSON
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode end: %v", err)
	}
	if body.SessionID != "sess-1" || body.EpisodeID != "ep-1" || body.ProviderSessionID != "prov-9" {
		t.Fatalf("end = %+v, want sess-1 on ep-1 with prov-9", body)
	}
	var stored string
	if err := db.Reader().QueryRowContext(t.Context(),
		"SELECT provider_session_id FROM sessions WHERE id = 'sess-1'").Scan(&stored); err != nil {
		t.Fatalf("read provider id: %v", err)
	}
	if stored != "prov-9" {
		t.Fatalf("provider id = %q, want prov-9", stored)
	}

	rec = end(cookie, "sess-1", `{"provider_session_id":"prov-9"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("repeat end status = %d, want 200", rec.Code)
	}

	foreign, _ := mintGuest(t, guests)
	rec = end(foreign, "sess-1", `{"provider_session_id":"prov-9"}`)
	if status, code := envelopeCode(t, rec); status != http.StatusNotFound || code != CodeSessionNotFound {
		t.Fatalf("foreign end status = %d code = %q, want 404 session_not_found", status, code)
	}

	rec = end(cookie, "missing", `{"provider_session_id":"prov-9"}`)
	if status, code := envelopeCode(t, rec); status != http.StatusNotFound || code != CodeSessionNotFound {
		t.Fatalf("missing end status = %d code = %q, want 404 session_not_found", status, code)
	}
}

// TestSessionEndAcceptsEmptyClose posts the empty close the browser
// sends and requires a 200 with the row stored. Malformed bodies still
// refuse loudly, and a repeat empty close stays harmless.
func TestSessionEndAcceptsEmptyClose(t *testing.T) {
	t.Parallel()
	db, guests := openDiary(t)
	cookie, owner := mintGuest(t, guests)
	seedEpisodeRow(t, db, "ep-1", owner.ID, 1, "recording")
	seedSessionRow(t, db, "sess-1", owner.ID, "ep-1")
	seedEpisodeRow(t, db, "ep-2", owner.ID, 2, "recording")
	seedSessionRow(t, db, "sess-2", owner.ID, "ep-2")
	stack := newEpisodeService(t, db, nil)
	handler := NewSessionEnd(stack.svc)

	post := func(sessionID string, bodyReader *strings.Reader) *httptest.ResponseRecorder {
		var req *http.Request
		if bodyReader == nil {
			req = httptest.NewRequest(http.MethodPost, "/api/sessions/"+sessionID+"/end", nil)
		} else {
			req = httptest.NewRequest(http.MethodPost, "/api/sessions/"+sessionID+"/end", bodyReader)
		}
		return serve(guests, handler, cookie, req)
	}
	stored := func(sessionID string) string {
		t.Helper()
		var got string
		if err := db.Reader().QueryRowContext(t.Context(),
			"SELECT provider_session_id FROM sessions WHERE id = ?", sessionID).Scan(&got); err != nil {
			t.Fatalf("read provider id: %v", err)
		}
		return got
	}

	rec := post("sess-1", strings.NewReader(""))
	if rec.Code != http.StatusOK {
		t.Fatalf("empty end status = %d, want 200", rec.Code)
	}
	var body sessionEndJSON
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode empty end: %v", err)
	}
	if body.SessionID != "sess-1" || body.EpisodeID != "ep-1" || body.ProviderSessionID != "" {
		t.Fatalf("empty end = %+v, want sess-1 on ep-1 with no provider id", body)
	}
	if got := stored("sess-1"); got != "" {
		t.Fatalf("provider id = %q, want empty", got)
	}

	rec = post("sess-2", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("nil body end status = %d, want 200", rec.Code)
	}
	if got := stored("sess-2"); got != "" {
		t.Fatalf("provider id = %q, want empty", got)
	}

	rec = post("sess-1", strings.NewReader(""))
	if rec.Code != http.StatusOK {
		t.Fatalf("repeat empty end status = %d, want 200", rec.Code)
	}

	for _, tc := range []struct {
		name string
		body string
	}{
		{"truncated object", "{"},
		{"wrong type", `{"provider_session_id":9}`},
		{"list shape", `[]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := post("sess-1", strings.NewReader(tc.body))
			if status, code := envelopeCode(t, rec); status != http.StatusBadRequest || code != CodeInvalidRequest {
				t.Fatalf("status = %d code = %q, want 400 invalid_request", status, code)
			}
		})
	}
}

// TestThreadsCountsComeFromRows seeds stored mentions across two episodes
// and requires the thread index to repeat their counts.
func TestThreadsCountsComeFromRows(t *testing.T) {
	t.Parallel()
	db, guests := openDiary(t)
	cookie, owner := mintGuest(t, guests)
	seedEpisodeRow(t, db, "ep-1", owner.ID, 1, "ready")
	seedEpisodeRow(t, db, "ep-2", owner.ID, 2, "ready")
	seedEpisodeRow(t, db, "ep-3", owner.ID, 3, "ready")
	seedMentionRow(t, db, "m-1", owner.ID, "ep-1", "person", "Mara", 3)
	seedMentionRow(t, db, "m-2", owner.ID, "ep-2", "person", "Mara", 7)
	seedMentionRow(t, db, "m-3", owner.ID, "ep-1", "person", "Theo", 1)
	seedMentionRow(t, db, "m-4", owner.ID, "ep-1", "keyphrase", "winter garden", 2)
	seedMentionRow(t, db, "m-5", owner.ID, "ep-2", "keyphrase", "winter garden", 4)
	seedMentionRow(t, db, "m-6", owner.ID, "ep-3", "keyphrase", "winter garden", 6)
	handler := NewThreads(db)

	rec := serve(guests, handler, cookie, httptest.NewRequest(http.MethodGet, "/api/threads", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("threads status = %d, want 200", rec.Code)
	}
	var body threadsJSON
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode threads: %v", err)
	}
	if len(body.NameThreads) != 1 {
		t.Fatalf("name threads = %+v, want only Mara", body.NameThreads)
	}
	mara := body.NameThreads[0]
	if mara.MentionCount != 2 || mara.EpisodeCount != 2 || mara.Display != "Mara" {
		t.Fatalf("mara = %+v, want 2 mentions across 2 episodes", mara)
	}
	if len(mara.Episodes) != 2 || mara.Episodes[0].EpisodeID != "ep-1" || mara.Episodes[1].EpisodeID != "ep-2" {
		t.Fatalf("mara hits = %+v, want ep-1 then ep-2", mara.Episodes)
	}
	if len(body.CircledTopics) != 1 || body.CircledTopics[0].EpisodeCount != 3 {
		t.Fatalf("circled = %+v, want winter garden across 3 episodes", body.CircledTopics)
	}

	foreign, _ := mintGuest(t, guests)
	rec = serve(guests, handler, foreign, httptest.NewRequest(http.MethodGet, "/api/threads", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("stranger threads status = %d, want 200", rec.Code)
	}
	var stranger threadsJSON
	if err := json.NewDecoder(rec.Body).Decode(&stranger); err != nil {
		t.Fatalf("decode stranger threads: %v", err)
	}
	if len(stranger.NameThreads) != 0 || len(stranger.CircledTopics) != 0 {
		t.Fatalf("stranger threads = %+v, want empty", stranger)
	}
}
