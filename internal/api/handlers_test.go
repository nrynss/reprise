package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	costsqlitestore "github.com/nrynss/keel/cost/sqlitestore"
	flagsqlitestore "github.com/nrynss/keel/flag/sqlitestore"
	"github.com/nrynss/keel/job"
	jobsqlitestore "github.com/nrynss/keel/job/sqlitestore"
	"github.com/nrynss/keel/lease"
	leasesqlitestore "github.com/nrynss/keel/lease/sqlitestore"
	"github.com/nrynss/keel/sqlite"
	"github.com/nrynss/reprise/internal/analysis"
	"github.com/nrynss/reprise/internal/broker"
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
	if err := analysis.Migrate(t.Context(), db); err != nil {
		t.Fatalf("open render link: %v", err)
	}
	if _, err := jobsqlitestore.Open(t.Context(), jobsqlitestore.Config{DB: db}); err != nil {
		t.Fatalf("open job rows: %v", err)
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
		{"empty body", "", http.StatusBadRequest, CodeInvalidRequest},
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
	if body.JobID != "job-1" || body.EpisodeID != "ep-1" || body.State != "rendering" || body.Queued {
		t.Fatalf("done = %+v, want job-1 on ep-1 rendering, not queued", body)
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

// TestMarkDoneQueuesBehindABusyRender marks a draft done while the render
// kind is full, on a service that waits. The answer is 202 with the
// render queued and no job, the episode waits in rendering, and a repeat
// tap is refused without a second start.
func TestMarkDoneQueuesBehindABusyRender(t *testing.T) {
	t.Parallel()
	db, guests := openDiary(t)
	cookie, owner := mintGuest(t, guests)
	seedEpisodeRow(t, db, "ep-1", owner.ID, 1, "draft")
	starter := &countingStarter{err: job.ErrLimit}
	svc, err := episode.NewService(episode.Config{
		DB:           db,
		Starter:      starter,
		RenderKind:   "render",
		Render:       stubRender{},
		WaitWhenBusy: true,
	})
	if err != nil {
		t.Fatalf("new episode service: %v", err)
	}
	handler := NewEpisodes(svc)
	done := func() *httptest.ResponseRecorder {
		return serve(guests, handler, cookie,
			httptest.NewRequest(http.MethodPost, "/api/episodes/ep-1/done", nil))
	}
	rec := done()
	if rec.Code != http.StatusAccepted {
		t.Fatalf("done status = %d, want 202: %s", rec.Code, rec.Body.String())
	}
	var body doneJSON
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode done: %v", err)
	}
	if !body.Queued || body.JobID != "" || body.State != "rendering" {
		t.Fatalf("done = %+v, want a queued render with no job, rendering", body)
	}
	var state string
	if err := db.Reader().QueryRowContext(t.Context(),
		"SELECT state FROM episodes WHERE id = 'ep-1'").Scan(&state); err != nil {
		t.Fatalf("read state: %v", err)
	}
	if state != "rendering" {
		t.Fatalf("state = %q, want rendering", state)
	}
	if status, code := envelopeCode(t, done()); status != http.StatusConflict || code != CodeIllegalTransition {
		t.Fatalf("repeat done status = %d code = %q, want 409 illegal_transition", status, code)
	}
	if starter.calls != 1 {
		t.Fatalf("starter calls = %d, want one", starter.calls)
	}
}

// TestMarkDoneReportsStarterFaults requires a full queue on a service
// that does not wait to answer 429, and a broken start to answer 503,
// with the episode failed.
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

	foreign, _ := mintGuest(t, guests)
	foreignReq := httptest.NewRequest(http.MethodPost, "/api/sessions/sess-1/end", strings.NewReader(""))
	rec = serve(guests, handler, foreign, foreignReq)
	if status, code := envelopeCode(t, rec); status != http.StatusNotFound || code != CodeSessionNotFound {
		t.Fatalf("foreign empty end status = %d code = %q, want 404 session_not_found", status, code)
	}
	rec = post("missing", strings.NewReader(""))
	if status, code := envelopeCode(t, rec); status != http.StatusNotFound || code != CodeSessionNotFound {
		t.Fatalf("missing empty end status = %d code = %q, want 404 session_not_found", status, code)
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

// settleMinter satisfies the broker mint seam without calling a provider.
type settleMinter struct{}

// Mint returns a fixed token. The close tests never redeem it.
func (settleMinter) Mint(context.Context, int) (string, error) {
	return "token", nil
}

// settleBuilder satisfies the broker config seam with an empty config.
type settleBuilder struct{}

// BuildSessionConfig returns an empty config. The close tests never read it.
func (settleBuilder) BuildSessionConfig(context.Context, string) (broker.SessionConfig, error) {
	return broker.SessionConfig{}, nil
}

// openSettleBroker builds a broker on db so a close test can read SettleInput.
// The constructor creates the linkage table. The test inserts the rows.
func openSettleBroker(t *testing.T, db *sqlite.DB) *broker.Broker {
	t.Helper()
	quiet := slog.New(slog.DiscardHandler)
	flags, err := flagsqlitestore.Open(t.Context(), flagsqlitestore.Config{DB: db, Logger: quiet})
	if err != nil {
		t.Fatalf("open flags: %v", err)
	}
	costs, err := costsqlitestore.Open(t.Context(), costsqlitestore.Config{DB: db, Limit: 1_000_000, Logger: quiet})
	if err != nil {
		t.Fatalf("open costs: %v", err)
	}
	leases, err := leasesqlitestore.Open(t.Context(), leasesqlitestore.Config{DB: db, Logger: quiet})
	if err != nil {
		t.Fatalf("open leases: %v", err)
	}
	quota, err := lease.NewQuota(4)
	if err != nil {
		t.Fatalf("open quota: %v", err)
	}
	diary, err := broker.NewSQLiteDiary(db)
	if err != nil {
		t.Fatalf("open diary: %v", err)
	}
	settled, err := broker.New(broker.Config{
		Flags:             flags,
		Budgets:           costsqlitestore.NewKeyedBudget(costs),
		DB:                db,
		LeaseQuota:        quota,
		LeaseStore:        leases,
		Minter:            settleMinter{},
		Sessions:          settleBuilder{},
		Diary:             diary,
		SessionCapSeconds: 1800,
		GuestMaxSessions:  4,
		OwnerSessionLimit: 1_000_000,
	})
	if err != nil {
		t.Fatalf("new broker: %v", err)
	}
	return settled
}

// seedSettleLink writes one mint linkage so SettleInput can read the close.
func seedSettleLink(t *testing.T, db *sqlite.DB, sessionID, ownerID, episodeID string) {
	t.Helper()
	raw, err := json.Marshal(costsqlitestore.Reservation{
		ID:        "res-" + sessionID,
		Amount:    1,
		ExpiresAt: time.Date(2026, 9, 22, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("encode reservation: %v", err)
	}
	if _, err := db.Writer().ExecContext(t.Context(),
		`INSERT INTO session_settle (session_id, owner_id, episode_id, lease_id, reservation, token_cap, minted_at)
		 VALUES (?, ?, ?, ?, ?, 1800, 1)`,
		sessionID, ownerID, episodeID, "lease-"+sessionID, string(raw)); err != nil {
		t.Fatalf("seed settle link %s: %v", sessionID, err)
	}
}

// steadyProviderSessionID reads the provider id on the recorded ready
// frame and the matching updated frame. The close of a connected take
// carries this id.
func steadyProviderSessionID(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "sessions", "steady", "events.json"))
	if err != nil {
		t.Fatalf("read recorded frames: %v", err)
	}
	var rows []struct {
		Event struct {
			Type      string `json:"type"`
			SessionID string `json:"session_id"`
			Config    struct {
				ID string `json:"id"`
			} `json:"config"`
		} `json:"event"`
	}
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatalf("decode recorded frames: %v", err)
	}
	var readyID, updatedID string
	for _, row := range rows {
		switch row.Event.Type {
		case "session.ready":
			if readyID == "" {
				readyID = row.Event.SessionID
			}
		case "session.updated":
			if updatedID == "" {
				updatedID = row.Event.Config.ID
			}
		}
	}
	if readyID == "" || readyID != updatedID {
		t.Fatalf("recorded provider id ready %q updated %q", readyID, updatedID)
	}
	return readyID
}

// TestSessionEndEmptyCloseKeepsStoredProvider posts an empty body after
// a shaped close and requires the stored id and the JSON echo to stay.
func TestSessionEndEmptyCloseKeepsStoredProvider(t *testing.T) {
	t.Parallel()
	db, guests := openDiary(t)
	cookie, owner := mintGuest(t, guests)
	seedEpisodeRow(t, db, "ep-1", owner.ID, 1, "recording")
	seedSessionRow(t, db, "sess-1", owner.ID, "ep-1")
	handler := NewSessionEnd(newEpisodeService(t, db, nil).svc)
	end := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/sessions/sess-1/end", strings.NewReader(body))
		return serve(guests, handler, cookie, req)
	}
	stored := func() string {
		t.Helper()
		var got string
		if err := db.Reader().QueryRowContext(t.Context(),
			"SELECT provider_session_id FROM sessions WHERE id = 'sess-1'").Scan(&got); err != nil {
			t.Fatalf("read provider id: %v", err)
		}
		return got
	}
	echo := func(rec *httptest.ResponseRecorder) sessionEndJSON {
		t.Helper()
		var body sessionEndJSON
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("decode end: %v", err)
		}
		return body
	}

	rec := end(`{"provider_session_id":"prov-9"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("shaped end status = %d, want 200", rec.Code)
	}
	if got := echo(rec); got.ProviderSessionID != "prov-9" || got.SessionID != "sess-1" || got.EpisodeID != "ep-1" {
		t.Fatalf("shaped end = %+v, want prov-9 on sess-1", got)
	}
	if got := stored(); got != "prov-9" {
		t.Fatalf("provider id = %q, want prov-9", got)
	}

	rec = end("")
	if rec.Code != http.StatusOK {
		t.Fatalf("empty end status = %d, want 200", rec.Code)
	}
	if got := echo(rec); got.ProviderSessionID != "prov-9" {
		t.Fatalf("empty echo = %q, want prov-9", got.ProviderSessionID)
	}
	if got := stored(); got != "prov-9" {
		t.Fatalf("provider id = %q, want prov-9 after an empty close", got)
	}

	rec = end(`{"provider_session_id":""}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("blank id status = %d, want 200", rec.Code)
	}
	if got := echo(rec); got.ProviderSessionID != "prov-9" {
		t.Fatalf("blank id echo = %q, want prov-9", got.ProviderSessionID)
	}
	if got := stored(); got != "prov-9" {
		t.Fatalf("provider id = %q, want prov-9 after a blank id", got)
	}

	rec = end("{")
	if status, code := envelopeCode(t, rec); status != http.StatusBadRequest || code != CodeInvalidRequest {
		t.Fatalf("malformed status = %d code = %q, want 400 invalid_request", status, code)
	}
	if got := stored(); got != "prov-9" {
		t.Fatalf("provider id = %q, want prov-9 after a refused body", got)
	}
}

// TestConnectedCloseStoresProviderForSettle posts the provider id a
// connected socket learned, then requires the row and the settle input
// to keep it. An empty close before any id stays a skip. An empty close
// after the id must not clear it.
func TestConnectedCloseStoresProviderForSettle(t *testing.T) {
	t.Parallel()
	db, guests := openDiary(t)
	cookie, owner := mintGuest(t, guests)
	seedEpisodeRow(t, db, "ep-early", owner.ID, 1, "recording")
	seedEpisodeRow(t, db, "ep-live", owner.ID, 2, "recording")
	seedSessionRow(t, db, "sess-early", owner.ID, "ep-early")
	seedSessionRow(t, db, "sess-live", owner.ID, "ep-live")
	handler := NewSessionEnd(newEpisodeService(t, db, nil).svc)
	providerID := steadyProviderSessionID(t)
	settled := openSettleBroker(t, db)
	seedSettleLink(t, db, "sess-early", owner.ID, "ep-early")
	seedSettleLink(t, db, "sess-live", owner.ID, "ep-live")

	post := func(sessionID, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+sessionID+"/end", strings.NewReader(body))
		return serve(guests, handler, cookie, req)
	}
	column := func(sessionID string) string {
		t.Helper()
		var got string
		if err := db.Reader().QueryRowContext(t.Context(),
			"SELECT provider_session_id FROM sessions WHERE id = ?", sessionID).Scan(&got); err != nil {
			t.Fatalf("read provider id: %v", err)
		}
		return got
	}

	rec := post("sess-early", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("early end status = %d, want 200", rec.Code)
	}
	var early sessionEndJSON
	if err := json.NewDecoder(rec.Body).Decode(&early); err != nil {
		t.Fatalf("decode early end: %v", err)
	}
	if early.ProviderSessionID != "" || column("sess-early") != "" {
		t.Fatalf("early close stored %q echo %q, want empty", column("sess-early"), early.ProviderSessionID)
	}
	if _, err := settled.SettleInput(t.Context(), "sess-early"); !errors.Is(err, broker.ErrNoClose) {
		t.Fatalf("early close settle err = %v, want ErrNoClose", err)
	}

	rec = post("sess-live", `{"provider_session_id":"`+providerID+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("connected end status = %d, want 200", rec.Code)
	}
	var connected sessionEndJSON
	if err := json.NewDecoder(rec.Body).Decode(&connected); err != nil {
		t.Fatalf("decode connected end: %v", err)
	}
	if connected.ProviderSessionID != providerID || column("sess-live") != providerID {
		t.Fatalf("connected close stored %q echo %q, want %q", column("sess-live"), connected.ProviderSessionID, providerID)
	}
	in, err := settled.SettleInput(t.Context(), "sess-live")
	if err != nil {
		t.Fatalf("settle input: %v, want the connected close", err)
	}
	if in.ProviderSessionID == "" {
		t.Fatal("connected close stored no provider id, and settle would skip it")
	}
	if in.ProviderSessionID != providerID {
		t.Fatalf("settle provider id = %q, want %q", in.ProviderSessionID, providerID)
	}

	rec = post("sess-live", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("empty after connected status = %d, want 200", rec.Code)
	}
	var again sessionEndJSON
	if err := json.NewDecoder(rec.Body).Decode(&again); err != nil {
		t.Fatalf("decode empty after connected: %v", err)
	}
	if again.ProviderSessionID != providerID || column("sess-live") != providerID {
		t.Fatalf("empty close stored %q echo %q, want %q", column("sess-live"), again.ProviderSessionID, providerID)
	}
	in, err = settled.SettleInput(t.Context(), "sess-live")
	if err != nil || in.ProviderSessionID != providerID {
		t.Fatalf("settle after empty close = %q err %v, want %q", in.ProviderSessionID, err, providerID)
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
