package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nrynss/keel/job"
	"github.com/nrynss/reprise/internal/api"
	"github.com/nrynss/reprise/internal/broker"
	"github.com/nrynss/reprise/internal/episode"
	"github.com/nrynss/reprise/internal/identity"
	"github.com/nrynss/reprise/internal/transcript"
)

// seedTerminalPass stores one terminal transcript pass stamped for an
// episode and fails the test on error.
func seedTerminalPass(t *testing.T, drafts *jobs, id, owner, episodeID string, status job.Status, errText string) {
	t.Helper()
	detail, err := json.Marshal(episodeDescriptor{OwnerID: owner, EpisodeID: episodeID})
	if err != nil {
		t.Fatalf("encode descriptor: %v", err)
	}
	rec := job.Record{
		ID:        id,
		Kind:      kindEditTranscript,
		Status:    status,
		Attempt:   1,
		RootID:    id,
		Progress:  job.Progress{Stage: "start", Detail: detail},
		UpdatedAt: time.Now(),
	}
	if errText != "" {
		rec.Err = errors.New(errText)
	}
	if err := drafts.store.Create(t.Context(), rec); err != nil {
		t.Fatalf("create pass %s: %v", id, err)
	}
}

// episodeState reads one episode state and fails the test on error.
func episodeState(t *testing.T, drafts *jobs, episodeID string) string {
	t.Helper()
	var state string
	if err := drafts.pipe.db.Reader().QueryRowContext(t.Context(),
		`SELECT state FROM episodes WHERE id = ?`, episodeID).Scan(&state); err != nil {
		t.Fatalf("read episode state: %v", err)
	}
	return state
}

// transcriptJobsFor counts the transcript jobs stamped for one episode
// and fails the test on error.
func transcriptJobsFor(t *testing.T, drafts *jobs, episodeID string) int {
	t.Helper()
	var count int
	if err := drafts.pipe.db.Reader().QueryRowContext(t.Context(),
		`SELECT COUNT(*) FROM jobs WHERE kind = ? AND progress LIKE ? ESCAPE '\'`,
		kindEditTranscript, `%"episode_id":"`+episodeID+`"%`).Scan(&count); err != nil {
		t.Fatalf("count passes: %v", err)
	}
	return count
}

// postCompletion posts one stem completion and decodes the answer,
// failing the test on any transport break.
func postCompletion(t *testing.T, handler http.Handler, cookie *http.Cookie, episodeID, userMedia, hostMedia string) (int, stemsCompleteResponse) {
	t.Helper()
	body, err := json.Marshal(stemsCompleteRequest{
		UserMediaID: userMedia, HostMediaID: hostMedia,
		UserSampleRate: 48000, HostSampleRate: 48000,
	})
	if err != nil {
		t.Fatalf("encode completion: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/episodes/"+episodeID+"/stems/complete", bytes.NewReader(body))
	req.AddCookie(cookie)
	req.SetPathValue("id", episodeID)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	var answer stemsCompleteResponse
	if err := json.NewDecoder(rec.Body).Decode(&answer); err != nil {
		t.Fatalf("decode completion answer: %v", err)
	}
	return rec.Code, answer
}

// completionFixture mints a guest, links both stems to its episode, and
// returns the completion handler with its scheduler, owner, and blob ids.
func completionFixture(t *testing.T, fx *wireFixture) (http.Handler, *jobs, *http.Cookie, string, string, string, string) {
	t.Helper()
	sessionBroker := openWireBroker(t, fx)
	identitySvc, err := identity.New(t.Context(), identity.Config{DB: fx.db, SigningKey: "wire-test-signing-key"})
	if err != nil {
		t.Fatalf("open identity: %v", err)
	}
	session, cookie := mintWireSession(t, fx, sessionBroker)
	if cookie == nil {
		t.Fatal("mint set no guest cookie")
	}
	var owner string
	if err := fx.db.Reader().QueryRowContext(t.Context(),
		`SELECT owner_id FROM sessions WHERE id = ?`, session.SessionID).Scan(&owner); err != nil {
		t.Fatalf("read session owner: %v", err)
	}
	ctx := t.Context()
	diary, err := broker.NewSQLiteDiary(fx.db)
	if err != nil {
		t.Fatalf("open diary: %v", err)
	}
	episodeID, _, err := diary.CreateEpisodeAndSession(ctx, owner, 1800)
	if err != nil {
		t.Fatalf("create episode: %v", err)
	}
	body := sineWAV()
	userID := persistStemAudio(t, fx, owner, episodeID, body)
	hostID := persistStemAudio(t, fx, owner, episodeID, body)
	linkStemRows(t, fx, owner, episodeID, userID, hostID)
	episodeSvc, err := episode.NewService(episode.Config{DB: fx.db, TranscriptKind: kindEditTranscript})
	if err != nil {
		t.Fatalf("new episode service: %v", err)
	}
	drafts := openDraftJobs(t, fx)
	handler := identitySvc.Middleware(newStemsComplete(episodeSvc, drafts, fx.db))
	return handler, drafts, cookie, episodeID, userID, hostID, owner
}

// TestRepeatCompletionReportsStandingOutcome replays the silent chain:
// a terminal transcript pass with no landed words, then two repeat
// completions. The first repeat fails the draft and reports the pass,
// the second repeat on the failed episode reports the same outcome,
// and neither starts a new job.
func TestRepeatCompletionReportsStandingOutcome(t *testing.T) {
	fx := openWireFixture(t)
	handler, drafts, cookie, episodeID, userID, hostID, owner := completionFixture(t, fx)
	ctx := t.Context()
	diarySvc, err := episode.NewService(episode.Config{DB: fx.db})
	if err != nil {
		t.Fatalf("new episode service: %v", err)
	}
	if _, err := diarySvc.CompleteStems(ctx, owner, episodeID, userID, hostID, 48000, 48000); err != nil {
		t.Fatalf("first completion: %v", err)
	}
	seedTerminalPass(t, drafts, "job-silent", owner, episodeID, job.StatusError, "provider refused")

	code, first := postCompletion(t, handler, cookie, episodeID, userID, hostID)
	if code != http.StatusOK {
		t.Fatalf("repeat status = %d, want 200", code)
	}
	if first.Moved || first.Scheduled || first.JobID != "" {
		t.Fatalf("repeat = %+v, want no move and no schedule", first)
	}
	if first.State != string(episode.StateFailed) {
		t.Fatalf("repeat state = %q, want failed", first.State)
	}
	if first.TranscriptOutcome == nil {
		t.Fatal("repeat carries no pass outcome, want the standing pass")
	}
	if first.TranscriptOutcome.JobID != "job-silent" ||
		first.TranscriptOutcome.Status != string(job.StatusError) ||
		first.TranscriptOutcome.Error != "provider refused" {
		t.Fatalf("outcome = %+v, want the silent pass with its text", first.TranscriptOutcome)
	}

	code, second := postCompletion(t, handler, cookie, episodeID, userID, hostID)
	if code != http.StatusOK {
		t.Fatalf("second repeat status = %d, want 200", code)
	}
	if second.Moved || second.Scheduled || second.JobID != "" {
		t.Fatalf("second repeat = %+v, want no move and no schedule", second)
	}
	if second.State != string(episode.StateFailed) {
		t.Fatalf("second repeat state = %q, want failed to stand", second.State)
	}
	if second.TranscriptOutcome == nil || *second.TranscriptOutcome != *first.TranscriptOutcome {
		t.Fatalf("second outcome = %+v, want the same standing pass as %+v", second.TranscriptOutcome, first.TranscriptOutcome)
	}
	if got := transcriptJobsFor(t, drafts, episodeID); got != 1 {
		t.Fatalf("transcript passes = %d, want only the seeded silent one", got)
	}
	if got := episodeState(t, drafts, episodeID); got != string(episode.StateFailed) {
		t.Fatalf("episode state = %q, want failed", got)
	}
}

// TestCoveredRepeatReportsRunningOutcome holds one running transcript
// pass over an episode and requires a repeat ensure to start nothing
// while the outcome names the covering pass. This is the earlier
// no-double-schedule pin, now reporting instead of silent.
func TestCoveredRepeatReportsRunningOutcome(t *testing.T) {
	fx := openWireFixture(t)
	const owner = "owner-covered-repeat"
	insertWireUser(t, fx, owner)
	drafts := openDraftJobs(t, fx)
	ctx := t.Context()
	episodeID := draftEpisode(t, fx, owner)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	blocker := func(ctx context.Context, _ func(job.Progress)) ([]byte, error) {
		select {
		case <-release:
			return []byte("{}"), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	covering, err := drafts.runner.StartKind(ctx, kindEditTranscript, blocker)
	if err != nil {
		t.Fatalf("start covering pass: %v", err)
	}
	if err := stampEpisode(ctx, drafts.store, covering, owner, episodeID); err != nil {
		t.Fatalf("stamp covering pass: %v", err)
	}
	if again, covered, err := drafts.ensureTranscript(ctx, owner, episodeID); err != nil || covered || again != "" {
		t.Fatalf("repeat ensure = %q, %v, %v, want no second start", again, covered, err)
	}
	out, err := episode.LastKindJob(ctx, drafts.pipe.db, episodeID, kindEditTranscript)
	if err != nil {
		t.Fatalf("read last pass: %v", err)
	}
	if !out.Found || out.JobID != covering {
		t.Fatalf("outcome = %+v, want the covering pass %q", out, covering)
	}
	if out.Status == string(job.StatusDone) || out.Status == string(job.StatusError) {
		t.Fatalf("outcome status = %q, want the pass still covering", out.Status)
	}
	if got := transcriptJobsFor(t, drafts, episodeID); got != 1 {
		t.Fatalf("transcript passes = %d, want only the covering one", got)
	}
}

// TestSettleTranscriptFailsEmptyPass drives the chain tail with an empty
// pass result and requires the episode to fail with a readable error,
// while a failed run and a worded run behave.
func TestSettleTranscriptFailsEmptyPass(t *testing.T) {
	fx := openWireFixture(t)
	const owner = "owner-settle-outcome"
	insertWireUser(t, fx, owner)
	drafts := openDraftJobs(t, fx)
	ctx := t.Context()
	emptyEpisode := draftEpisode(t, fx, owner)
	empty, err := json.Marshal(transcript.Result{TranscriptID: "tx-empty", Words: 0})
	if err != nil {
		t.Fatalf("encode empty result: %v", err)
	}
	if _, err := drafts.settleTranscript(ctx, owner, emptyEpisode, empty, nil); err == nil {
		t.Fatal("empty settlement error is nil, want the wordless failure")
	} else if !strings.Contains(err.Error(), "stored no words") {
		t.Fatalf("empty settlement error = %q, want the wordless text", err)
	}
	if got := episodeState(t, drafts, emptyEpisode); got != string(episode.StateFailed) {
		t.Fatalf("empty episode state = %q, want failed", got)
	}

	brokenEpisode := draftEpisode(t, fx, owner)
	boom := errors.New("provider refused")
	if _, err := drafts.settleTranscript(ctx, owner, brokenEpisode, nil, boom); !errors.Is(err, boom) {
		t.Fatalf("broken settlement error = %v, want the run error", err)
	}
	if got := episodeState(t, drafts, brokenEpisode); got != string(episode.StateFailed) {
		t.Fatalf("broken episode state = %q, want failed", got)
	}

	wordedEpisode := draftEpisode(t, fx, owner)
	worded, err := json.Marshal(transcript.Result{TranscriptID: "tx-words", Words: 3})
	if err != nil {
		t.Fatalf("encode worded result: %v", err)
	}
	if _, err := drafts.settleTranscript(ctx, owner, wordedEpisode, worded, nil); err != nil {
		t.Fatalf("worded settlement: %v", err)
	}
	var state string
	if err := fx.db.Reader().QueryRowContext(ctx,
		`SELECT state FROM episodes WHERE id = ?`, wordedEpisode).Scan(&state); err != nil {
		t.Fatalf("read worded state: %v", err)
	}
	if state != string(episode.StateRecording) {
		t.Fatalf("worded episode state = %q, want recording to stand", state)
	}
}

// TestCompletionAnswerMatchesDetail serves the detail beside the
// completion answer after a failed pass and requires both to name the
// same outcome.
func TestCompletionAnswerMatchesDetail(t *testing.T) {
	fx := openWireFixture(t)
	handler, drafts, cookie, episodeID, userID, hostID, owner := completionFixture(t, fx)
	seedTerminalPass(t, drafts, "job-voice", owner, episodeID, job.StatusError, "provider refused")

	code, answer := postCompletion(t, handler, cookie, episodeID, userID, hostID)
	if code != http.StatusOK {
		t.Fatalf("completion status = %d, want 200", code)
	}
	if answer.TranscriptOutcome == nil {
		t.Fatal("completion carries no pass outcome, want the failed pass")
	}

	identitySvc, err := identity.New(t.Context(), identity.Config{DB: fx.db, SigningKey: "wire-test-signing-key"})
	if err != nil {
		t.Fatalf("open identity: %v", err)
	}
	episodeSvc, err := episode.NewService(episode.Config{DB: fx.db, TranscriptKind: kindEditTranscript})
	if err != nil {
		t.Fatalf("new episode service: %v", err)
	}
	detail := identitySvc.Middleware(api.NewEpisodes(episodeSvc))
	req := httptest.NewRequest(http.MethodGet, "/api/episodes/"+episodeID, nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	detail.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("detail status = %d, want 200", rec.Code)
	}
	var body struct {
		Proposals []struct {
			ID string `json:"id"`
		} `json:"proposals"`
		TranscriptOutcome *transcriptOutcomeJSON `json:"transcript_outcome"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if body.TranscriptOutcome == nil {
		t.Fatal("detail carries no pass outcome, want the failed pass")
	}
	if *body.TranscriptOutcome != *answer.TranscriptOutcome {
		t.Fatalf("detail outcome = %+v, want the completion outcome %+v", body.TranscriptOutcome, answer.TranscriptOutcome)
	}
}
