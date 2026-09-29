// Account deletion for signed in owners.
//
// Deleting needs a fresh sign in code typed within the last ten minutes.
// The code check reuses the sign in verification, so one rule decides
// every code and the deletion consumes the code it checked. The deletion
// then erases every episode through the episode erasure fan-out and
// removes the identity rows, the code rows, every session row and the
// user row. No stored address survives.
//
// The work runs as one job under AccountName, so a long diary outlasts
// its request and a restart resumes what is left. Register AccountKinds
// before the runner opens, the way the erasure kind registers, because
// a kind that declares no resume work never resumes after a restart.

package privacy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/nrynss/keel/erase"
	"github.com/nrynss/keel/job"
	"github.com/nrynss/keel/mediastore"
	"github.com/nrynss/keel/sqlite"
	"github.com/nrynss/reprise/internal/identity"
)

// AccountName is the job kind account deletion runs under. Register it
// beside the episode erasure kind before the runner opens, so a restart
// resumes an interrupted deletion from its snapshot.
const AccountName = "account-delete"

// Account deletion bounds. Every target repeats safely, so every restart
// may resume the work. Three erase rounds per episode match the retention
// sweep, and one minute bounds each inner erasure watch.
const (
	accountSnapshotVersion = 1
	accountMaxAttempts     = 5
	accountEraseRounds     = 3
	accountWatchTimeout    = 60 * time.Second
	accountWatchTick       = 20 * time.Millisecond
	accountBodyMax         = 1 << 20
)

// Sentinel errors. Every failure path this file produces wraps one of
// these, so callers branch with errors.Is.
var (
	// ErrReauth reports a deletion code the server refuses. The code is
	// unknown, requested by another session, expired, used, past its
	// attempts, or bound to another user. Callers answer 401.
	ErrReauth = errors.New("privacy: account deletion code refused")
	// ErrIncomplete reports a deletion job that ended with an episode
	// still owed. The message names the episode it could not finish.
	ErrIncomplete = errors.New("privacy: account deletion incomplete")
	// ErrTakeSaving reports a deletion asked while a take still settles.
	// The money must land first, so the caller tries again in a minute.
	// Handlers answer 409.
	ErrTakeSaving = errors.New("privacy: account deletion waits on a settling take")
)

// CodeVerifier checks one fresh sign in code. The sign in service
// implements it. Deletion declares it here, so tests bind a stub and
// the one verification rule never forks.
type CodeVerifier interface {
	// VerifyLoginCode checks one code and resolves the login.
	VerifyLoginCode(ctx context.Context, sessionID, userID, email, code, choice string) (identity.LoginOutcome, error)
}

// DeleteAccount starts the deletion of userID and returns the job id at
// once. The caller follows the job to done. It first gates ownership,
// then proves the address belongs to this user, then refuses while a take
// still settles, then consumes one fresh code through the sign in
// verification. A refusal deletes nothing, and a settle refusal never
// burns the code. The job erases every episode through the erasure
// fan-out, clears the settle books, then removes the stray media, the
// code rows, the session rows, the identity rows and the user row.
func (s *Service) DeleteAccount(ctx context.Context, verifier CodeVerifier, sessionID, userID, email, code string) (string, error) {
	r := s.runnerOf()
	if r == nil {
		return "", fmt.Errorf("privacy: delete account: %w: no runner bound", ErrInvalid)
	}
	if verifier == nil || sessionID == "" || userID == "" || strings.TrimSpace(email) == "" || strings.TrimSpace(code) == "" {
		return "", fmt.Errorf("privacy: delete account: %w: deletion needs a verifier, a session, a user, an address and a code", ErrInvalid)
	}
	if !s.owns.Owns(ctx, userID) {
		return "", fmt.Errorf("privacy: delete account for %s: %w", userID, ErrNotOwner)
	}
	if err := s.ownsAddress(ctx, userID, email); err != nil {
		return "", err
	}
	if err := s.accountSettled(ctx, userID); err != nil {
		return "", err
	}
	outcome, err := verifier.VerifyLoginCode(ctx, sessionID, userID, email, code, "")
	if err != nil {
		if errors.Is(err, identity.ErrInvalidCode) || errors.Is(err, identity.ErrDiaryConflict) {
			return "", fmt.Errorf("privacy: delete account for %s: %w: %w", userID, ErrReauth, err)
		}
		return "", fmt.Errorf("privacy: delete account for %s: %w", userID, err)
	}
	if outcome.UserID != userID {
		return "", fmt.Errorf("privacy: delete account for %s: %w: sign in moved devices", userID, ErrReauth)
	}
	episodes, err := s.accountEpisodes(ctx, userID)
	if err != nil {
		return "", err
	}
	snap := accountSnapshot{Version: accountSnapshotVersion, User: userID, Episodes: episodes}
	id, err := r.StartKind(ctx, AccountName, func(ctx context.Context, progress func(job.Progress)) ([]byte, error) {
		return s.runAccount(ctx, r, progress, snap)
	})
	if err != nil {
		return "", fmt.Errorf("privacy: delete account for %s: %w", userID, err)
	}
	return id, nil
}

// ownsAddress reports whether the address names an identity on userID.
// It reads the normalized subject the sign in flow stores, so a code for
// a stranger never reaches verification and never burns.
func (s *Service) ownsAddress(ctx context.Context, userID, email string) error {
	address := strings.ToLower(strings.TrimSpace(email))
	var holder string
	err := s.db.Reader().QueryRowContext(ctx,
		"SELECT user_id FROM identities WHERE provider = ? AND subject = ?", "email", address).Scan(&holder)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("privacy: delete account for %s: %w", userID, ErrNotOwner)
	}
	if err != nil {
		return fmt.Errorf("privacy: delete account for %s: read identity: %w", userID, err)
	}
	if holder != userID {
		return fmt.Errorf("privacy: delete account for %s: %w", userID, ErrNotOwner)
	}
	return nil
}

// AccountKind returns the job kind that runs account deletions. Register
// it under AccountName before the runner opens, so a restart resumes an
// interrupted deletion. Deleting repeats safely, because every target
// counts a missing row, blob, file, session or transcript as confirmed.
func (s *Service) AccountKind() job.Kind {
	return job.Kind{Idempotent: true, MaxAttempts: accountMaxAttempts, Resume: s.resumeAccount}
}

// AccountKinds returns every job kind account deletion needs: the
// deletion kind under AccountName and the inner erasure kind under its
// own name. Pass the result into the runner config, because an erasure
// kind that declares no resume work never resumes after a restart.
func (s *Service) AccountKinds() map[string]job.Kind {
	return map[string]job.Kind{AccountName: s.AccountKind(), erase.KindName: s.Eraser().Kind()}
}

// accountEpisode is one user episode the deletion must erase, with the
// inner erasure jobs it already started. Done marks an episode with
// nothing left owed.
type accountEpisode struct {
	// Episode is the episode id.
	Episode string `json:"episode"`
	// Erasures holds inner erasure job ids, oldest first.
	Erasures []string `json:"erasures,omitempty"`
	// Done marks an episode with nothing left owed.
	Done bool `json:"done,omitempty"`
}

// accountSnapshot is the ledger one deletion progress report carries. A
// run that resumes reads it from the interrupted record, so only
// unfinished episodes erase again and stuck erasures retry from their
// recorded ids.
type accountSnapshot struct {
	// Version guards the encoding. Only version one exists.
	Version int `json:"version"`
	// User is the deleted user id. Every delete scopes to it.
	User string `json:"user"`
	// Episodes lists every episode the user owned at inventory time.
	Episodes []accountEpisode `json:"episodes"`
	// RowsDone marks a user with no identity, code, session or user row
	// left.
	RowsDone bool `json:"rows_done,omitempty"`
}

// accountEpisodes lists the episode ids one user owns, in id order.
func (s *Service) accountEpisodes(ctx context.Context, user string) ([]accountEpisode, error) {
	rows, err := s.db.Reader().QueryContext(ctx,
		"SELECT id FROM episodes WHERE owner_id = ? ORDER BY id", user)
	if err != nil {
		return nil, fmt.Errorf("privacy: delete account for %s: list episodes: %w", user, err)
	}
	defer func() { _ = rows.Close() }()
	var out []accountEpisode
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("privacy: delete account for %s: list episodes: %w", user, err)
		}
		out = append(out, accountEpisode{Episode: id})
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("privacy: delete account for %s: list episodes: %w", user, err)
	}
	return out, nil
}

// resumeAccount rebuilds the work of a deletion a restart left
// unfinished. It is the job kind resume hook. A record with no snapshot
// fails, because nothing recorded names the deleted user.
func (s *Service) resumeAccount(rec job.Record) (job.Func, error) {
	snap := accountSnapshot{}
	if len(rec.Progress.Detail) == 0 {
		return nil, fmt.Errorf("privacy: resume deletion %s: %w: unknown snapshot", rec.ID, ErrInvalid)
	}
	if err := json.Unmarshal(rec.Progress.Detail, &snap); err != nil {
		return nil, fmt.Errorf("privacy: resume deletion %s: %w", rec.ID, err)
	}
	if snap.Version != accountSnapshotVersion || snap.User == "" {
		return nil, fmt.Errorf("privacy: resume deletion %s: %w: unknown snapshot", rec.ID, ErrInvalid)
	}
	return func(ctx context.Context, progress func(job.Progress)) ([]byte, error) {
		r := s.runnerOf()
		if r == nil {
			return nil, fmt.Errorf("privacy: delete account: %w: no runner bound", ErrInvalid)
		}
		return s.runAccount(ctx, r, progress, snap)
	}, nil
}

// runAccount drives one deletion attempt on runner. Snap names the user
// an earlier step inventoried. The attempt threads runner through every
// step and never re-reads the bound field, so a bind that lands
// mid-attempt cannot move the running work.
func (s *Service) runAccount(ctx context.Context, runner *job.Runner, progress func(job.Progress), snap accountSnapshot) ([]byte, error) {
	s.publishAccount(progress, snap)
	if err := s.accountSettled(ctx, snap.User); err != nil {
		return nil, err
	}
	for i := range snap.Episodes {
		if snap.Episodes[i].Done {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("privacy: delete account for %s: %w", snap.User, err)
		}
		report := func() { s.publishAccount(progress, snap) }
		if err := s.eraseAccountEpisode(ctx, runner, snap.User, &snap.Episodes[i], report); err != nil {
			return nil, err
		}
		s.publishAccount(progress, snap)
	}
	if err := s.deleteAccountSettle(ctx, snap.User); err != nil {
		return nil, err
	}
	if err := s.deleteAccountStrays(ctx, snap.User); err != nil {
		return nil, err
	}
	if err := s.deleteAccountRows(ctx, snap.User); err != nil {
		return nil, err
	}
	snap.RowsDone = true
	s.publishAccount(progress, snap)
	return nil, nil
}

// eraseAccountEpisode erases one episode through the episode erasure and
// waits for it to read complete. A gone episode row finishes from the
// recorded erasures, because only the recorded ref still names the
// provider copies. Ownership already passed at request time, so this
// reuses the inventory and the fan-out directly with the recorded owner
// scope instead of the request user check, which a background job
// cannot carry.
func (s *Service) eraseAccountEpisode(ctx context.Context, runner *job.Runner, user string, episode *accountEpisode, report func()) error {
	if episode.Done {
		return nil
	}
	if !s.accountEpisodePresent(ctx, episode.Episode) {
		return s.finishRecordedAccountErasure(ctx, runner, episode, report)
	}
	for round := 1; round <= accountEraseRounds; round++ {
		id, err := s.startAccountErasure(ctx, runner, user, episode)
		if err != nil {
			return err
		}
		if episode.Done {
			report()
			return nil
		}
		episode.Erasures = append(episode.Erasures, id)
		report()
		if err := s.waitAccountErasure(ctx, runner, id); err != nil {
			return err
		}
		rep, err := s.eraser.Inspect(ctx, runner, id)
		if err != nil {
			return fmt.Errorf("privacy: delete account: inspect erasure for %s: %w", episode.Episode, err)
		}
		if rep.Complete() {
			episode.Done = true
			report()
			return nil
		}
	}
	return fmt.Errorf("privacy: delete account: erase %s: %w: episode still owed", episode.Episode, ErrIncomplete)
}

// startAccountErasure starts a fresh episode erasure, or restarts the
// latest recorded one when the episode already owes provider deletes. A
// recorded erasure that already reads complete marks the episode done
// without new work.
func (s *Service) startAccountErasure(ctx context.Context, runner *job.Runner, user string, episode *accountEpisode) (string, error) {
	if n := len(episode.Erasures); n > 0 {
		last := episode.Erasures[n-1]
		if rep, err := s.eraser.Inspect(ctx, runner, last); err == nil {
			if rep.Complete() {
				episode.Done = true
				return "", nil
			}
			if targets, serr := s.source(ctx, rep.Ref); serr == nil {
				if next, serr := s.eraser.Start(ctx, runner, rep.Ref, targets); serr == nil {
					return next, nil
				}
			}
		}
	}
	ref, err := s.inventory(ctx, user, episode.Episode)
	if err != nil {
		return "", fmt.Errorf("privacy: delete account: list %s: %w", episode.Episode, err)
	}
	raw, err := json.Marshal(ref)
	if err != nil {
		return "", fmt.Errorf("privacy: delete account: encode %s: %w", episode.Episode, err)
	}
	next, err := s.eraser.Start(ctx, runner, string(raw), s.targets(ref))
	if err != nil {
		return "", fmt.Errorf("privacy: delete account: erase %s: %w", episode.Episode, err)
	}
	return next, nil
}

// finishRecordedAccountErasure finishes an episode whose row is already
// gone. Complete recorded erasures read as done. Any other recorded
// erasure restarts from its ref and waits, because only the recorded ref
// still names the provider copies. An episode with no recorded erasure
// went through another flow, so it reads as done.
func (s *Service) finishRecordedAccountErasure(ctx context.Context, runner *job.Runner, episode *accountEpisode, report func()) error {
	if len(episode.Erasures) == 0 {
		episode.Done = true
		return nil
	}
	for _, id := range episode.Erasures {
		rep, err := s.eraser.Inspect(ctx, runner, id)
		if err != nil {
			return fmt.Errorf("privacy: delete account: inspect erasure for %s: %w", episode.Episode, err)
		}
		if rep.Complete() {
			continue
		}
		if err := s.retryRecordedAccountErasure(ctx, runner, episode, rep.Ref, report); err != nil {
			return err
		}
	}
	episode.Done = true
	report()
	return nil
}

// retryRecordedAccountErasure restarts one stuck erasure from its
// recorded ref and waits for it to read complete.
func (s *Service) retryRecordedAccountErasure(ctx context.Context, runner *job.Runner, episode *accountEpisode, ref string, report func()) error {
	for round := 1; round <= accountEraseRounds; round++ {
		targets, err := s.source(ctx, ref)
		if err != nil {
			return err
		}
		next, err := s.eraser.Start(ctx, runner, ref, targets)
		if err != nil {
			return fmt.Errorf("privacy: delete account: re-erase %s: %w", episode.Episode, err)
		}
		episode.Erasures = append(episode.Erasures, next)
		report()
		if err := s.waitAccountErasure(ctx, runner, next); err != nil {
			return err
		}
		rep, err := s.eraser.Inspect(ctx, runner, next)
		if err != nil {
			return fmt.Errorf("privacy: delete account: inspect erasure for %s: %w", episode.Episode, err)
		}
		if rep.Complete() {
			return nil
		}
		ref = rep.Ref
	}
	return fmt.Errorf("privacy: delete account: erase %s: %w: episode still owed", episode.Episode, ErrIncomplete)
}

// waitAccountErasure polls one inner erasure until its job lands. Done
// means the erasure confirmed every target. Any other terminal means the
// provider still owes deletes, so the caller retries from the recorded
// ref.
func (s *Service) waitAccountErasure(ctx context.Context, runner *job.Runner, id string) error {
	deadline := time.Now().Add(accountWatchTimeout)
	for {
		attempts, err := runner.Attempts(ctx, id)
		if err != nil {
			return fmt.Errorf("privacy: delete account: watch erasure: %w", err)
		}
		for _, rec := range attempts {
			switch rec.Status {
			case job.StatusDone:
				return nil
			case job.StatusError, job.StatusCancelled, job.StatusInterrupted:
				return fmt.Errorf("privacy: delete account: erasure %s ended %s", id, rec.Status)
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("privacy: delete account: erasure %s never landed: %w", id, ErrIncomplete)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("privacy: delete account: watch erasure: %w", ctx.Err())
		case <-time.After(accountWatchTick):
		}
	}
}

// accountEpisodePresent reports whether the episode row still exists.
func (s *Service) accountEpisodePresent(ctx context.Context, episode string) bool {
	var total int
	err := s.db.Reader().QueryRowContext(ctx,
		"SELECT COUNT(*) FROM episodes WHERE id = ?", episode).Scan(&total)
	return err == nil && total > 0
}

// deleteAccountStrays deletes every media blob the user still owns.
// Episode erasures already removed grouped blobs, so survivors are
// ungrouped uploads. A missing blob reads as done, because deleting
// repeats safely.
func (s *Service) deleteAccountStrays(ctx context.Context, user string) error {
	rows, err := s.db.Reader().QueryContext(ctx, "SELECT id FROM media WHERE owner = ?", user)
	if err != nil {
		return fmt.Errorf("privacy: delete account for %s: list stray media: %w", user, err)
	}
	defer func() { _ = rows.Close() }()
	var blobs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return fmt.Errorf("privacy: delete account for %s: list stray media: %w", user, err)
		}
		blobs = append(blobs, id)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("privacy: delete account for %s: list stray media: %w", user, err)
	}
	for _, blob := range blobs {
		if err := s.media.Delete(ctx, blob); err != nil {
			if errors.Is(err, mediastore.ErrNotFound) {
				continue
			}
			return fmt.Errorf("privacy: delete account for %s: delete stray %s: %w", user, blob, err)
		}
	}
	return nil
}

// deleteAccountRows removes the code rows, the session rows, the
// identity rows and the user row of one user. Codes go before sessions,
// because they reference them. Sessions and identities go before the
// user, because they reference it. Every episode is already erased, so
// no episode references the user anymore. Every delete repeats safely.
func (s *Service) deleteAccountRows(ctx context.Context, user string) error {
	if _, err := s.db.Writer().ExecContext(ctx,
		"DELETE FROM login_codes WHERE requesting_session IN (SELECT id FROM guest_sessions WHERE user_id = ?)", user); err != nil {
		return fmt.Errorf("privacy: delete account for %s: delete code rows: %w", user, err)
	}
	if _, err := s.db.Writer().ExecContext(ctx,
		"DELETE FROM guest_sessions WHERE user_id = ?", user); err != nil {
		return fmt.Errorf("privacy: delete account for %s: delete session rows: %w", user, err)
	}
	if _, err := s.db.Writer().ExecContext(ctx,
		"DELETE FROM identities WHERE user_id = ?", user); err != nil {
		return fmt.Errorf("privacy: delete account for %s: delete identity rows: %w", user, err)
	}
	if _, err := s.db.Writer().ExecContext(ctx,
		"DELETE FROM users WHERE id = ?", user); err != nil {
		return fmt.Errorf("privacy: delete account for %s: delete user row: %w", user, err)
	}
	return nil
}

// accountSettled reports whether every live take of user settled its
// money. It reads the settle linkage of diary sessions that still exist.
// A link whose session row is already gone names an erased episode, so
// its leftover rows delete with the account instead of blocking it. A
// live link with no settled claim blocks with ErrTakeSaving. A database
// the settle path never touched holds no links, so it reads as settled.
func (s *Service) accountSettled(ctx context.Context, user string) error {
	if !accountTableExists(ctx, s.db, "session_settle") {
		return nil
	}
	var live int
	err := s.db.Reader().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM session_settle l
		 JOIN sessions s ON s.id = l.session_id WHERE l.owner_id = ?`, user).Scan(&live)
	if err != nil {
		return fmt.Errorf("privacy: delete account for %s: list unsettled takes: %w", user, err)
	}
	if live == 0 {
		return nil
	}
	if !accountTableExists(ctx, s.db, "reconcile_state") {
		return fmt.Errorf("privacy: delete account for %s: %w", user, ErrTakeSaving)
	}
	var open int
	err = s.db.Reader().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM session_settle l
		 JOIN sessions s ON s.id = l.session_id
		 LEFT JOIN reconcile_state r ON r.session_id = l.session_id
		 WHERE l.owner_id = ? AND (r.settled IS NULL OR r.settled = 0)`, user).Scan(&open)
	if err != nil {
		return fmt.Errorf("privacy: delete account for %s: list unsettled takes: %w", user, err)
	}
	if open > 0 {
		return fmt.Errorf("privacy: delete account for %s: %w", user, ErrTakeSaving)
	}
	return nil
}

// deleteAccountSettle removes the settle books of user once every take
// settled. The linkage goes last, because it scopes the first two
// deletes. The spend ledger stays as it is, so the global total never
// moves. Owner ceilings and lease rows stay where the shared library
// keeps them, because no library call drops them.
func (s *Service) deleteAccountSettle(ctx context.Context, user string) error {
	if !accountTableExists(ctx, s.db, "session_settle") {
		return nil
	}
	if accountTableExists(ctx, s.db, "reconcile_state") {
		if _, err := s.db.Writer().ExecContext(ctx,
			`DELETE FROM reconcile_state WHERE session_id IN
			 (SELECT session_id FROM session_settle WHERE owner_id = ?)`, user); err != nil {
			return fmt.Errorf("privacy: delete account for %s: delete settle claims: %w", user, err)
		}
	}
	if accountTableExists(ctx, s.db, "sweep_state") {
		if _, err := s.db.Writer().ExecContext(ctx,
			`DELETE FROM sweep_state WHERE session_id IN
			 (SELECT session_id FROM session_settle WHERE owner_id = ?)`, user); err != nil {
			return fmt.Errorf("privacy: delete account for %s: delete sweep rows: %w", user, err)
		}
	}
	if _, err := s.db.Writer().ExecContext(ctx,
		`DELETE FROM session_settle WHERE owner_id = ?`, user); err != nil {
		return fmt.Errorf("privacy: delete account for %s: delete settle links: %w", user, err)
	}
	return nil
}

// accountTableExists reports whether name holds a table in this database.
// Settle tables arrive with their own packages, so deletion skips one
// that never ran instead of failing the whole account.
func accountTableExists(ctx context.Context, db *sqlite.DB, name string) bool {
	var found string
	err := db.Reader().QueryRowContext(ctx,
		"SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?", name).Scan(&found)
	return err == nil && found == name
}

// publishAccount records one deletion ledger snapshot through the job
// progress channel, so the runner streams it and leaves it durable in
// the record.
func (s *Service) publishAccount(progress func(job.Progress), snap accountSnapshot) {
	detail, err := json.Marshal(snap)
	if err != nil {
		return
	}
	current := int64(doneAccountEpisodes(snap.Episodes))
	total := int64(len(snap.Episodes))
	progress(job.Progress{Stage: "account", Current: &current, Total: &total, Detail: detail})
}

// doneAccountEpisodes counts episodes with nothing left owed.
func doneAccountEpisodes(episodes []accountEpisode) int {
	total := 0
	for _, episode := range episodes {
		if episode.Done {
			total++
		}
	}
	return total
}

// Envelope codes the deletion screen branches on. Codes stay stable,
// wording stays free.
const (
	// CodeAccountInvalidCode answers a code the server refuses. The code
	// is unknown, requested by another session, expired, used, past its
	// attempts, or bound to another user. One code covers every case, so
	// the answer never names an account.
	CodeAccountInvalidCode = "invalid_code"
	// CodeAccountInvalidRequest answers a malformed body or a value the
	// route cannot honour.
	CodeAccountInvalidRequest = "invalid_request"
	// CodeAccountTakeSaving answers a deletion asked while a take still
	// settles. The caller tries again in a minute, after the money lands.
	CodeAccountTakeSaving = "take_saving"
)

// PatternAccountDelete removes the caller's account through a deletion
// job. Mount the handler once behind the guest session middleware, so
// every request carries a user and a session.
const PatternAccountDelete = "/api/account/delete"

// accountDeleteRequestJSON carries the fresh code that authorizes a
// deletion. The code must be live and requested by this same session.
type accountDeleteRequestJSON struct {
	// Email is the address the code went to, before normalization.
	Email string `json:"email"`
	// Code is the typed six digit value.
	Code string `json:"code"`
}

// accountDeleteResponse is the deletion body. JobID is the deletion job
// the caller follows to done over the job stream.
type accountDeleteResponse struct {
	// JobID is the deletion job id.
	JobID string `json:"job_id"`
}

// AccountHandler returns the account deletion endpoint on one handler.
// Mount it behind the guest session middleware and the spend gate. The
// verifier is the sign in service, which checks the fresh code.
func (s *Service) AccountHandler(verifier CodeVerifier) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(PatternAccountDelete, func(w http.ResponseWriter, r *http.Request) {
		s.serveAccountDelete(w, r, verifier)
	})
	return mux
}

// serveAccountDelete answers POST by starting the deletion and returning
// the job id. The job does the deleting, so the handler never waits on
// provider calls. A refused code answers 401 without saying which guard
// tripped. A stranger answers the same 404 as a missing episode.
func (s *Service) serveAccountDelete(w http.ResponseWriter, r *http.Request, verifier CodeVerifier) {
	if r.Method != http.MethodPost {
		writeRefusal(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "account deletion uses POST")
		return
	}
	user, ok := identity.UserFromContext(r.Context())
	sessionID, sok := identity.SessionIDFromContext(r.Context())
	if !ok || !sok || user.ID == "" || sessionID == "" {
		writeRefusal(w, http.StatusInternalServerError, CodeInternal, "the guest session is not wired")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, accountBodyMax)
	var body accountDeleteRequestJSON
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeRefusal(w, http.StatusBadRequest, CodeAccountInvalidRequest, "this request carries no usable code")
		return
	}
	if strings.TrimSpace(body.Email) == "" || strings.TrimSpace(body.Code) == "" {
		writeRefusal(w, http.StatusBadRequest, CodeAccountInvalidRequest, "this request carries no usable code")
		return
	}
	jobID, err := s.DeleteAccount(r.Context(), verifier, sessionID, user.ID, body.Email, body.Code)
	if err != nil {
		switch {
		case errors.Is(err, ErrReauth):
			writeRefusal(w, http.StatusUnauthorized, CodeAccountInvalidCode, "this code is expired, used, or never requested on this device")
		case errors.Is(err, ErrTakeSaving):
			writeRefusal(w, http.StatusConflict, CodeAccountTakeSaving, "Your last take is still being saved. Try again in a minute.")
		case errors.Is(err, ErrNotOwner):
			writeRefusal(w, http.StatusNotFound, CodeNotFound, "that account opens nothing")
		default:
			writeRefusal(w, http.StatusInternalServerError, CodeInternal, "that request could not finish")
		}
		return
	}
	clearSessionCookie(w)
	writeJSON(w, http.StatusAccepted, accountDeleteResponse{JobID: jobID})
}

// clearSessionCookie drops the session cookie, because the deletion
// revokes every session the user holds. The next request mints a fresh
// guest, so the device keeps a working diary.
func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     identity.CookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
	})
}
