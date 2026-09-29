// Account deletion pins. A user with two episodes and two identities is
// deleted through a fresh sign in code, and every table plus the media
// directory plus the provider doubles prove nothing of hers remains. A
// second owner stands untouched. Refused codes delete nothing.

package privacy_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nrynss/keel/id"
	"github.com/nrynss/keel/job"
	"github.com/nrynss/keel/stream"
	"github.com/nrynss/reprise/internal/identity"
	"github.com/nrynss/reprise/internal/mail"
	"github.com/nrynss/reprise/internal/privacy"
)

// accountAddress is the address the deleted user signs in with. The
// deletion test types it padded and mixed case, so the run proves the
// code, the identity lookup and the verification read one address.
const accountAddress = "owner-a@example.com"

// openAccountCase builds the diary fixture with the identity tables on
// the same database, two guest sessions for owner-a, and an email plus
// a Google identity on her. It rebinds the service to a runner carrying
// the deletion kind beside the erasure kind.
func openAccountCase(t *testing.T) (*fixture, *identity.Service, *mail.Fake, string, string) {
	t.Helper()
	fx := openFixture(t)
	fake := &mail.Fake{}
	idsvc, err := identity.New(t.Context(), identity.Config{
		DB:           fx.db,
		SigningKey:   "test-session-signing-key-with-length",
		LoginCodeKey: "test-login-code-key-with-length",
		Mail:         fake,
	})
	if err != nil {
		t.Fatalf("open identity service: %v", err)
	}
	runner, err := job.Open(t.Context(), job.Config{
		Broker: stream.New(stream.Config{}),
		Store:  fx.jobStore,
		Kinds:  fx.svc.AccountKinds(),
	})
	if err != nil {
		t.Fatalf("open account runner: %v", err)
	}
	if err := fx.svc.BindRunner(runner); err != nil {
		t.Fatalf("bind account runner: %v", err)
	}
	fx.runner = runner
	sessA1, err := id.New()
	if err != nil {
		t.Fatalf("mint session id: %v", err)
	}
	sessA2, err := id.New()
	if err != nil {
		t.Fatalf("mint session id: %v", err)
	}
	fx.exec(t, `INSERT INTO guest_sessions (id, user_id, created_at, revoked) VALUES (?, ?, 1, 0)`, sessA1, "owner-a")
	fx.exec(t, `INSERT INTO guest_sessions (id, user_id, created_at, revoked) VALUES (?, ?, 1, 0)`, sessA2, "owner-a")
	mailID, err := id.New()
	if err != nil {
		t.Fatalf("mint identity id: %v", err)
	}
	googleID, err := id.New()
	if err != nil {
		t.Fatalf("mint identity id: %v", err)
	}
	fx.exec(t, `INSERT INTO identities (id, user_id, provider, subject, created_at) VALUES (?, ?, 'email', ?, 1)`,
		mailID, "owner-a", accountAddress)
	fx.exec(t, `INSERT INTO identities (id, user_id, provider, subject, created_at) VALUES (?, ?, 'google', ?, 1)`,
		googleID, "owner-a", "google-sub-owner-a")
	return fx, idsvc, fake, sessA1, sessA2
}

// seedSecondEpisode writes a second full episode for owner: session,
// stems, render, analysis, word, turn, proposal with its decision,
// mention with its callback, cover, and the reconciliation row naming
// the stereo copy. It mirrors the fixture seeder without the user row,
// so one user holds two episodes.
func seedSecondEpisode(t *testing.T, fx *fixture, owner string, number int64) *episodeSeed {
	t.Helper()
	ctx := t.Context()
	episodeID, err := id.New()
	if err != nil {
		t.Fatalf("mint episode id: %v", err)
	}
	seed := &episodeSeed{episode: episodeID, owner: owner}
	token, err := id.New()
	if err != nil {
		t.Fatalf("mint share token: %v", err)
	}
	if _, err := fx.db.Writer().ExecContext(ctx, `INSERT INTO episodes
		(id, owner_id, number, title, state, visibility, share_token, seeded)
		VALUES (?, ?, ?, ?, 'ready', 'private', ?, 0)`,
		episodeID, owner, number, "Second episode for "+owner, token); err != nil {
		t.Fatalf("seed episode: %v", err)
	}
	seed.session = "prov-session-" + owner + "-second"
	if _, err := fx.db.Writer().ExecContext(ctx, `INSERT INTO sessions
		(id, owner_id, episode_id, provider_session_id, token_cap, connected_seconds)
		VALUES (?, ?, ?, ?, 1800, 60)`, "sess-second-"+owner, owner, episodeID, seed.session); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	for _, role := range []string{"user", "host"} {
		blob := fx.persist(t, owner, episodeID, "audio/ogg", []byte("second stem "+role+" "+owner))
		seed.stems = append(seed.stems, blob)
		stemID, err := id.New()
		if err != nil {
			t.Fatalf("mint stem id: %v", err)
		}
		if _, err := fx.db.Writer().ExecContext(ctx, `INSERT INTO stems
			(id, owner_id, episode_id, media_id, role, sample_rate, start_offset_ms)
			VALUES (?, ?, ?, ?, ?, 48000, 0)`, stemID, owner, episodeID, blob, role); err != nil {
			t.Fatalf("seed stem: %v", err)
		}
	}
	seed.opus = fx.persist(t, owner, episodeID, "audio/ogg", []byte("second render opus "+owner))
	seed.aac = fx.persist(t, owner, episodeID, "audio/mp4", []byte("second render aac "+owner))
	renderID, err := id.New()
	if err != nil {
		t.Fatalf("mint render id: %v", err)
	}
	if _, err := fx.db.Writer().ExecContext(ctx, `INSERT INTO renders
		(id, owner_id, episode_id, input_hash, opus_media_id, aac_media_id, loudness)
		VALUES (?, ?, ?, 'hash', ?, ?, -16)`, renderID, owner, episodeID, seed.opus, seed.aac); err != nil {
		t.Fatalf("seed render: %v", err)
	}
	seed.stereo = fx.persist(t, owner, episodeID, "audio/ogg", []byte("second stereo copy "+owner))
	seed.batch = "batch-second-" + owner
	analysisID, err := id.New()
	if err != nil {
		t.Fatalf("mint analysis id: %v", err)
	}
	if _, err := fx.db.Writer().ExecContext(ctx, `INSERT INTO analyses
		(id, owner_id, episode_id, transcript_id, chapters, summary, entities, key_phrases)
		VALUES (?, ?, ?, ?, '', '', '', '')`, analysisID, owner, episodeID, seed.batch); err != nil {
		t.Fatalf("seed analysis: %v", err)
	}
	for table, value := range map[string]string{
		"words": "second hello", "turns": "second hello there",
	} {
		rowID, err := id.New()
		if err != nil {
			t.Fatalf("mint row id: %v", err)
		}
		switch table {
		case "words":
			fx.exec(t, `INSERT INTO words
				(id, owner_id, episode_id, text, start_ms, end_ms, source)
				VALUES (?, ?, ?, ?, 0, 120, 'rendered')`, rowID, owner, episodeID, value)
		case "turns":
			fx.exec(t, `INSERT INTO turns
				(id, owner_id, episode_id, role, text, started_ms, ended_ms, provider_item_id)
				VALUES (?, ?, ?, 'host', ?, 0, 120, '')`, rowID, owner, episodeID, value)
		}
	}
	proposalID, err := id.New()
	if err != nil {
		t.Fatalf("mint proposal id: %v", err)
	}
	fx.exec(t, `INSERT INTO proposals
		(id, owner_id, episode_id, kind, start_word, end_word, reason)
		VALUES (?, ?, ?, 'cut', 0, 1, 'a pause')`, proposalID, owner, episodeID)
	decisionID, err := id.New()
	if err != nil {
		t.Fatalf("mint decision id: %v", err)
	}
	fx.exec(t, `INSERT INTO decisions
		(id, owner_id, episode_id, proposal_id, decision)
		VALUES (?, ?, ?, ?, 'keep')`, decisionID, owner, episodeID, proposalID)
	mentionID, err := id.New()
	if err != nil {
		t.Fatalf("mint mention id: %v", err)
	}
	fx.exec(t, `INSERT INTO mentions
		(id, owner_id, episode_id, kind, word_offset, quote)
		VALUES (?, ?, ?, 'person', 0, 'hello')`, mentionID, owner, episodeID)
	callbackID, err := id.New()
	if err != nil {
		t.Fatalf("mint callback id: %v", err)
	}
	fx.exec(t, `INSERT INTO callbacks
		(id, owner_id, episode_id, mention_id, used)
		VALUES (?, ?, ?, ?, 0)`, callbackID, owner, episodeID, mentionID)
	seed.coverFile = episodeID + ".png"
	if err := os.WriteFile(filepath.Join(fx.coverDir, seed.coverFile), []byte("second cover "+owner), 0o600); err != nil {
		t.Fatalf("seed cover: %v", err)
	}
	fx.exec(t, `INSERT INTO reconcile_state (session_id, recording_media_id, updated_at)
		VALUES (?, ?, 1)`, "sess-second-"+owner, seed.stereo)
	return seed
}

// accountCodeFromMail pulls the six digit code from the most recent fake
// mail. The mail names the code before any other number, so the first
// digit run is the code.
func accountCodeFromMail(t *testing.T, fake *mail.Fake) string {
	t.Helper()
	msgs := fake.Messages()
	if len(msgs) == 0 {
		t.Fatal("mail fake recorded no message")
	}
	var digits strings.Builder
	for _, r := range msgs[len(msgs)-1].Text {
		if r >= '0' && r <= '9' {
			digits.WriteRune(r)
		} else if digits.Len() > 0 {
			break
		}
	}
	if digits.Len() != 6 {
		t.Fatalf("mail held %q, want one six digit code", msgs[len(msgs)-1].Text)
	}
	return digits.String()
}

// countUserRows counts identity, session and code rows for owner. The
// login tables key by user or by session, so each needs its own query.
func countUserRows(t *testing.T, fx *fixture, owner, sessA1, sessA2 string) (identities, sessions, codes int) {
	t.Helper()
	ctx := t.Context()
	if err := fx.db.Reader().QueryRowContext(ctx,
		"SELECT COUNT(*) FROM identities WHERE user_id = ?", owner).Scan(&identities); err != nil {
		t.Fatalf("count identities: %v", err)
	}
	if err := fx.db.Reader().QueryRowContext(ctx,
		"SELECT COUNT(*) FROM guest_sessions WHERE user_id = ?", owner).Scan(&sessions); err != nil {
		t.Fatalf("count guest sessions: %v", err)
	}
	if err := fx.db.Reader().QueryRowContext(ctx,
		"SELECT COUNT(*) FROM login_codes WHERE requesting_session IN (?, ?)", sessA1, sessA2).Scan(&codes); err != nil {
		t.Fatalf("count login codes: %v", err)
	}
	return identities, sessions, codes
}

// TestDeleteAccountRemovesEverything pins the deletion half. A user with
// two episodes and two identities is deleted through a fresh code, and
// afterwards no table, no media file, no cover and no provider copy of
// hers remains, while the second owner stands untouched row by row.
func TestDeleteAccountRemovesEverything(t *testing.T) {
	fx, idsvc, fake, sessA1, sessA2 := openAccountCase(t)
	second := seedSecondEpisode(t, fx, "owner-a", 2)
	first := fx.seeds["owner-a"]

	if err := idsvc.RequestLoginCode(t.Context(), sessA1, accountAddress); err != nil {
		t.Fatalf("request code: %v", err)
	}
	code := accountCodeFromMail(t, fake)

	fx.as("owner-a")
	jobID, err := fx.svc.DeleteAccount(t.Context(), idsvc, sessA1, "owner-a", "  Owner-A@Example.com  ", code)
	if err != nil {
		t.Fatalf("delete account: %v", err)
	}
	if jobID == "" {
		t.Fatal("delete account returned an empty job id")
	}
	fx.waitJobDone(t, jobID)

	for _, table := range contentTables {
		if got := fx.count(t, table, "owner-a"); got != 0 {
			t.Fatalf("table %s holds %d owner-a rows, want none", table, got)
		}
	}
	for _, table := range []string{"users", "identities", "guest_sessions"} {
		var got int
		column := "id"
		if table != "users" {
			column = "user_id"
		}
		if err := fx.db.Reader().QueryRowContext(t.Context(),
			"SELECT COUNT(*) FROM "+table+" WHERE "+column+" = ?", "owner-a").Scan(&got); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if got != 0 {
			t.Fatalf("table %s holds %d owner-a rows, want none", table, got)
		}
	}
	if _, _, codes := countUserRows(t, fx, "owner-a", sessA1, sessA2); codes != 0 {
		t.Fatalf("login codes hold %d owner-a rows, want none", codes)
	}
	var reconciled int
	if err := fx.db.Reader().QueryRowContext(t.Context(),
		"SELECT COUNT(*) FROM reconcile_state WHERE session_id IN (?, ?)", "sess-owner-a", "sess-second-owner-a").Scan(&reconciled); err != nil {
		t.Fatalf("count reconcile rows: %v", err)
	}
	if reconciled != 0 {
		t.Fatalf("reconcile_state holds %d owner-a rows, want none", reconciled)
	}
	var mediaRows int
	if err := fx.db.Reader().QueryRowContext(t.Context(),
		"SELECT COUNT(*) FROM media WHERE owner = ?", "owner-a").Scan(&mediaRows); err != nil {
		t.Fatalf("count media rows: %v", err)
	}
	if mediaRows != 0 {
		t.Fatalf("media index holds %d owner-a rows, want none", mediaRows)
	}
	for _, seed := range []*episodeSeed{first, second} {
		for _, blob := range append(append([]string{}, seed.stems...), seed.opus, seed.aac, seed.stereo) {
			if _, err := os.Stat(filepath.Join(fx.mediaDir, blob)); !os.IsNotExist(err) {
				t.Fatalf("blob file %s still on disk (err %v)", blob, err)
			}
			if got := signedOutMedia(fx, t, blob).Code; got != http.StatusNotFound {
				t.Fatalf("erased blob %s status %d, want 404", blob, got)
			}
		}
		if _, err := os.Stat(filepath.Join(fx.coverDir, seed.coverFile)); !os.IsNotExist(err) {
			t.Fatalf("cover file %s still on disk (err %v)", seed.coverFile, err)
		}
		if !fx.sessions.ended(seed.session) {
			t.Fatalf("provider session %s never ended", seed.session)
		}
		after, err := fx.transcript.Get(t.Context(), seed.batch)
		if err != nil {
			t.Fatalf("fetch transcript %s: %v", seed.batch, err)
		}
		if !after.Deleted() {
			t.Fatalf("provider transcript %s still readable", seed.batch)
		}
	}

	stayedCounts := map[string]int{
		"episodes": 1, "sessions": 1, "stems": 2, "renders": 1,
		"analyses": 1, "words": 1, "users": 1,
	}
	for table, want := range stayedCounts {
		if got := fx.count(t, table, "owner-b"); got != want {
			t.Fatalf("table %s holds %d owner-b rows, want %d", table, got, want)
		}
	}
	stayed := fx.seeds["owner-b"]
	for _, blob := range append(append([]string{}, stayed.stems...), stayed.opus, stayed.aac, stayed.stereo) {
		if _, err := os.Stat(filepath.Join(fx.mediaDir, blob)); err != nil {
			t.Fatalf("owner-b blob %s missing (err %v)", blob, err)
		}
	}
	if fx.sessions.ended(stayed.session) {
		t.Fatal("second owner session ended, want it untouched")
	}
	after, err := fx.transcript.Get(t.Context(), stayed.batch)
	if err != nil {
		t.Fatalf("fetch owner-b transcript: %v", err)
	}
	if after.Deleted() {
		t.Fatal("second owner transcript deleted, want it kept")
	}
	rep, err := fx.svc.Eraser().Inspect(t.Context(), fx.runner, jobID)
	if err == nil && rep.Complete() {
		t.Fatal("deletion job read as an erasure, want its own kind")
	}
}

// TestDeleteAccountRefusesBadCode pins the re-authentication half. A
// wrong code, a code requested by another session, and an address with
// no identity on this user each refuse, and every row survives.
func TestDeleteAccountRefusesBadCode(t *testing.T) {
	fx, idsvc, fake, sessA1, sessA2 := openAccountCase(t)
	fx.as("owner-a")

	if err := idsvc.RequestLoginCode(t.Context(), sessA1, accountAddress); err != nil {
		t.Fatalf("request code: %v", err)
	}
	code := accountCodeFromMail(t, fake)

	if _, err := fx.svc.DeleteAccount(t.Context(), idsvc, sessA1, "owner-a", accountAddress, "000000"); !errors.Is(err, privacy.ErrReauth) {
		t.Fatalf("wrong code err %v, want re-authentication refused", err)
	}
	if _, err := fx.svc.DeleteAccount(t.Context(), idsvc, sessA2, "owner-a", accountAddress, code); !errors.Is(err, privacy.ErrReauth) {
		t.Fatalf("foreign session err %v, want re-authentication refused", err)
	}
	if _, err := fx.svc.DeleteAccount(t.Context(), idsvc, sessA1, "owner-a", "stranger@example.com", code); !errors.Is(err, privacy.ErrNotOwner) {
		t.Fatalf("stranger address err %v, want not owner", err)
	}

	if got := fx.count(t, "episodes", "owner-a"); got != 1 {
		t.Fatalf("episodes holds %d owner-a rows, want 1", got)
	}
	if got := fx.count(t, "users", "owner-a"); got != 1 {
		t.Fatalf("users holds %d owner-a rows, want 1", got)
	}
	if identities, sessions, _ := countUserRows(t, fx, "owner-a", sessA1, sessA2); identities != 2 || sessions != 2 {
		t.Fatalf("identities %d sessions %d, want 2 and 2", identities, sessions)
	}
}

// stubVerifier answers code checks with a scripted outcome, so the
// handler tests run without mail.
type stubVerifier struct {
	outcome     identity.LoginOutcome
	err         error
	calls       int
	seenSession string
	seenUser    string
	seenEmail   string
	seenCode    string
}

// VerifyLoginCode records the call and answers the scripted outcome.
func (s *stubVerifier) VerifyLoginCode(_ context.Context, sessionID, userID, email, code, _ string) (identity.LoginOutcome, error) {
	s.calls++
	s.seenSession = sessionID
	s.seenUser = userID
	s.seenEmail = email
	s.seenCode = code
	return s.outcome, s.err
}

// mintHandlerGuest mints one guest through the identity middleware and
// returns its user, session and cookie.
func mintHandlerGuest(t *testing.T, idsvc *identity.Service) (identity.User, string, *http.Cookie) {
	t.Helper()
	var user identity.User
	var session string
	spy := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		user, _ = identity.UserFromContext(r.Context())
		session, _ = identity.SessionIDFromContext(r.Context())
	})
	rec := httptest.NewRecorder()
	idsvc.Middleware(spy).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if user.ID == "" || session == "" {
		t.Fatal("middleware minted no guest")
	}
	var cookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == identity.CookieName {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("middleware set no session cookie")
	}
	return user, session, cookie
}

// postAccountDelete posts one deletion body to the handler with the
// session cookie.
func postAccountDelete(t *testing.T, handler http.Handler, cookie *http.Cookie, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, privacy.PatternAccountDelete, strings.NewReader(body))
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

// TestAccountHandlerStartsJob pins the endpoint. A fresh code starts the
// deletion job, clears the session cookie, and the job removes the user
// rows. A refused code answers 401 and changes nothing, and a stranger
// answers 404 without waking the verifier.
func TestAccountHandlerStartsJob(t *testing.T) {
	fx := openFixture(t)
	runner, err := job.Open(t.Context(), job.Config{
		Broker: stream.New(stream.Config{}),
		Store:  fx.jobStore,
		Kinds:  fx.svc.AccountKinds(),
	})
	if err != nil {
		t.Fatalf("open account runner: %v", err)
	}
	if err := fx.svc.BindRunner(runner); err != nil {
		t.Fatalf("bind account runner: %v", err)
	}
	fx.runner = runner
	idsvc, err := identity.New(t.Context(), identity.Config{
		DB:         fx.db,
		SigningKey: "test-session-signing-key-with-length",
	})
	if err != nil {
		t.Fatalf("open identity service: %v", err)
	}
	user, session, cookie := mintHandlerGuest(t, idsvc)
	mailID, err := id.New()
	if err != nil {
		t.Fatalf("mint identity id: %v", err)
	}
	fx.exec(t, `INSERT INTO identities (id, user_id, provider, subject, created_at) VALUES (?, ?, 'email', ?, 1)`,
		mailID, user.ID, "guest@example.com")
	stub := &stubVerifier{outcome: identity.LoginOutcome{UserID: user.ID, SessionID: "next-session"}}
	handler := idsvc.Middleware(fx.svc.AccountHandler(stub))

	fx.as(user.ID)
	rec := postAccountDelete(t, handler, cookie, `{"email":"guest@example.com","code":"123456"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("delete status %d, want 202: %s", rec.Code, rec.Body.String())
	}
	var answered struct {
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &answered); err != nil || answered.JobID == "" {
		t.Fatalf("delete answer %q, want a job id", rec.Body.String())
	}
	if stub.calls != 1 || stub.seenSession != session || stub.seenUser != user.ID ||
		stub.seenEmail != "guest@example.com" || stub.seenCode != "123456" {
		t.Fatalf("verifier saw %+v, want this session, user, address and code", stub)
	}
	cleared := false
	for _, c := range rec.Result().Cookies() {
		if c.Name == identity.CookieName && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Fatal("deletion answer kept the session cookie, want it cleared")
	}
	fx.waitJobDone(t, answered.JobID)
	if got := fx.count(t, "users", user.ID); got != 0 {
		t.Fatalf("users holds %d rows for the deleted user, want none", got)
	}

	refused := &stubVerifier{err: identity.ErrInvalidCode}
	handler = idsvc.Middleware(fx.svc.AccountHandler(refused))
	user2, _, cookie2 := mintHandlerGuest(t, idsvc)
	mailID2, err := id.New()
	if err != nil {
		t.Fatalf("mint identity id: %v", err)
	}
	fx.exec(t, `INSERT INTO identities (id, user_id, provider, subject, created_at) VALUES (?, ?, 'email', ?, 1)`,
		mailID2, user2.ID, "guest@example.com")
	fx.as(user2.ID)
	rec = postAccountDelete(t, handler, cookie2, `{"email":"guest@example.com","code":"000000"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("refused delete status %d, want 401: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "invalid_code") {
		t.Fatalf("refused delete answer %q, want the invalid code", rec.Body.String())
	}
	if got := fx.count(t, "users", user2.ID); got != 1 {
		t.Fatalf("users holds %d rows after a refused delete, want 1", got)
	}

	quiet := &stubVerifier{outcome: identity.LoginOutcome{UserID: user2.ID}}
	handler = idsvc.Middleware(fx.svc.AccountHandler(quiet))
	fx.as("stranger")
	rec = postAccountDelete(t, handler, cookie2, `{"email":"guest@example.com","code":"123456"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("stranger delete status %d, want 404: %s", rec.Code, rec.Body.String())
	}
	if quiet.calls != 0 {
		t.Fatal("stranger delete woke the verifier, want no call")
	}
}
