// Erasure for diary episodes.
//
// Erasing removes every place a recording landed. The episode row goes,
// and its delete cascades to every diary content row beneath it. Every
// media blob grouped under the episode goes, including the provider
// stereo copy the reconciler persisted. The cover file goes. The
// provider session and every remaining batch transcript go through the
// provider clients.
//
// The work runs as one erasure job, so every target retries until it
// confirms and a restart resumes what is left. The erase ref carries
// the full inventory, because the rows are already gone when a resumed
// run rebuilds its targets. Every target is safe to repeat: a missing
// row, blob, file, session, or transcript counts as confirmed.

package privacy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"

	"github.com/nrynss/keel/erase"
	"github.com/nrynss/keel/mediastore"
)

// eraseRef is the application key the erasure carries. It names every
// target the fan-out must delete, so a run that resumes after a restart
// rebuilds the same list from the job record instead of from rows that
// are already gone.
type eraseRef struct {
	// Version guards the encoding. Only version one exists.
	Version int `json:"version"`
	// Episode is the erased episode.
	Episode string `json:"episode"`
	// Owner scopes every delete to one owner.
	Owner string `json:"owner"`
	// Blobs holds every media id grouped under the episode.
	Blobs []string `json:"blobs"`
	// Session is the provider voice session id. Empty means none.
	Session string `json:"session,omitempty"`
	// Transcripts holds remaining provider batch transcript ids.
	Transcripts []string `json:"transcripts,omitempty"`
}

// inventory reads every deletable thing one episode owns. It runs once,
// before the erasure starts, while the rows still name their blobs.
func (s *Service) inventory(ctx context.Context, ownerID, episodeID string) (eraseRef, error) {
	ref := eraseRef{Version: 1, Episode: episodeID, Owner: ownerID}
	seen := map[string]bool{}
	add := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			ref.Blobs = append(ref.Blobs, id)
		}
	}
	rows, err := s.db.Reader().QueryContext(ctx,
		"SELECT media_id FROM stems WHERE episode_id = ? AND owner_id = ?", episodeID, ownerID)
	if err != nil {
		return eraseRef{}, fmt.Errorf("privacy: erase %s: list stems: %w", episodeID, err)
	}
	for rows.Next() {
		var blob string
		if err := rows.Scan(&blob); err != nil {
			_ = rows.Close()
			return eraseRef{}, fmt.Errorf("privacy: erase %s: list stems: %w", episodeID, err)
		}
		add(blob)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return eraseRef{}, fmt.Errorf("privacy: erase %s: list stems: %w", episodeID, err)
	}
	renderRows, err := s.db.Reader().QueryContext(ctx,
		"SELECT opus_media_id, aac_media_id FROM renders WHERE episode_id = ? AND owner_id = ?",
		episodeID, ownerID)
	if err != nil {
		return eraseRef{}, fmt.Errorf("privacy: erase %s: list renders: %w", episodeID, err)
	}
	for renderRows.Next() {
		var opus, aac string
		if err := renderRows.Scan(&opus, &aac); err != nil {
			_ = renderRows.Close()
			return eraseRef{}, fmt.Errorf("privacy: erase %s: list renders: %w", episodeID, err)
		}
		add(opus)
		add(aac)
	}
	_ = renderRows.Close()
	if err := renderRows.Err(); err != nil {
		return eraseRef{}, fmt.Errorf("privacy: erase %s: list renders: %w", episodeID, err)
	}
	grouped, err := s.db.Reader().QueryContext(ctx,
		"SELECT id FROM media WHERE media_group = ? AND owner = ?", episodeID, ownerID)
	if err != nil {
		return eraseRef{}, fmt.Errorf("privacy: erase %s: list media group: %w", episodeID, err)
	}
	for grouped.Next() {
		var blob string
		if err := grouped.Scan(&blob); err != nil {
			_ = grouped.Close()
			return eraseRef{}, fmt.Errorf("privacy: erase %s: list media group: %w", episodeID, err)
		}
		add(blob)
	}
	_ = grouped.Close()
	if err := grouped.Err(); err != nil {
		return eraseRef{}, fmt.Errorf("privacy: erase %s: list media group: %w", episodeID, err)
	}
	var session sql.NullString
	err = s.db.Reader().QueryRowContext(ctx,
		"SELECT provider_session_id FROM sessions WHERE episode_id = ? AND owner_id = ? LIMIT 1",
		episodeID, ownerID).Scan(&session)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return eraseRef{}, fmt.Errorf("privacy: erase %s: list session: %w", episodeID, err)
	}
	if session.Valid {
		ref.Session = session.String
	}
	txRows, err := s.db.Reader().QueryContext(ctx,
		"SELECT transcript_id FROM analyses WHERE episode_id = ? AND owner_id = ?", episodeID, ownerID)
	if err != nil {
		return eraseRef{}, fmt.Errorf("privacy: erase %s: list transcripts: %w", episodeID, err)
	}
	for txRows.Next() {
		var transcript string
		if err := txRows.Scan(&transcript); err != nil {
			_ = txRows.Close()
			return eraseRef{}, fmt.Errorf("privacy: erase %s: list transcripts: %w", episodeID, err)
		}
		if transcript != "" {
			ref.Transcripts = append(ref.Transcripts, transcript)
		}
	}
	_ = txRows.Close()
	if err := txRows.Err(); err != nil {
		return eraseRef{}, fmt.Errorf("privacy: erase %s: list transcripts: %w", episodeID, err)
	}
	sort.Strings(ref.Blobs)
	sort.Strings(ref.Transcripts)
	return ref, nil
}

// targets rebuilds the delete list from one ref. Names stay stable
// across restarts, because they derive from the ref alone and never
// from rows a previous attempt already removed.
func (s *Service) targets(ref eraseRef) []erase.Target {
	out := []erase.Target{rowsTarget{svc: s, ref: ref}}
	for _, blob := range ref.Blobs {
		out = append(out, blobTarget{svc: s, blob: blob})
	}
	out = append(out, coverTarget{svc: s, ref: ref})
	if ref.Session != "" {
		out = append(out, sessionTarget{svc: s, ref: ref})
	}
	for _, transcript := range ref.Transcripts {
		out = append(out, transcriptTarget{svc: s, transcript: transcript})
	}
	return out
}

// source rebuilds one erasure from its ref after a restart. The library
// owns the fan-out and the ledger, and only this product names the
// places a recording landed, so only this function can rebuild them.
func (s *Service) source(ctx context.Context, ref string) ([]erase.Target, error) {
	var decoded eraseRef
	if err := json.Unmarshal([]byte(ref), &decoded); err != nil {
		return nil, fmt.Errorf("privacy: rebuild erase: %w", err)
	}
	if decoded.Version != 1 || decoded.Episode == "" || decoded.Owner == "" {
		return nil, fmt.Errorf("privacy: rebuild erase: %w: broken ref", ErrInvalid)
	}
	return s.targets(decoded), nil
}

// Erase starts the erasure of episodeID and returns the job id at once.
// The caller follows the job to done. A missing episode reports
// ErrNotFound, and another owner's episode reports ErrNotOwner, so the
// handler answers 404 for both.
func (s *Service) Erase(ctx context.Context, episodeID string) (string, error) {
	if episodeID == "" {
		return "", fmt.Errorf("privacy: erase: %w: empty episode", ErrInvalid)
	}
	ep, err := s.owned(ctx, episodeID)
	if err != nil {
		return "", err
	}
	ref, err := s.inventory(ctx, ep.owner, ep.id)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(ref)
	if err != nil {
		return "", fmt.Errorf("privacy: erase %s: %w", episodeID, err)
	}
	jobID, err := s.eraser.Start(ctx, s.runner, string(raw), s.targets(ref))
	if err != nil {
		return "", fmt.Errorf("privacy: erase %s: %w", episodeID, err)
	}
	return jobID, nil
}

// rowsTarget deletes the episode row. The delete cascades to every
// diary content row beneath it and to the reconciliation rows that
// name its sessions, so one target clears every table at once. A row
// that is already gone counts as confirmed, because deleting repeats
// safely.
type rowsTarget struct {
	svc *Service
	ref eraseRef
}

// Name identifies the target in progress reports.
func (t rowsTarget) Name() string { return "rows" }

// Delete removes the episode row with everything under it. The owner
// guard sits inside the delete, so a racing owner change cannot widen
// it to another owner's episode.
func (t rowsTarget) Delete(ctx context.Context) error {
	tx, err := t.svc.db.Writer().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("privacy: erase rows %s: %w", t.ref.Episode, err)
	}
	defer func() { _ = tx.Rollback() }()
	if t.tableExists(ctx, tx, "reconcile_state") {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM reconcile_state WHERE session_id IN
			 (SELECT id FROM sessions WHERE episode_id = ? AND owner_id = ?)`,
			t.ref.Episode, t.ref.Owner); err != nil {
			return fmt.Errorf("privacy: erase rows %s: %w", t.ref.Episode, err)
		}
	}
	res, err := tx.ExecContext(ctx,
		"DELETE FROM episodes WHERE id = ? AND owner_id = ?", t.ref.Episode, t.ref.Owner)
	if err != nil {
		return fmt.Errorf("privacy: erase rows %s: %w", t.ref.Episode, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("privacy: erase rows %s: %w", t.ref.Episode, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("privacy: erase rows %s: %w", t.ref.Episode, err)
	}
	if affected == 0 {
		return fmt.Errorf("privacy: erase rows %s: %w", t.ref.Episode, erase.ErrGone)
	}
	return nil
}

// tableExists reports whether name holds a table in this database. The
// reconciliation table arrives with its own package, so erasure skips
// it when that package never ran instead of failing the whole delete.
func (t rowsTarget) tableExists(ctx context.Context, tx *sql.Tx, name string) bool {
	var found string
	err := tx.QueryRowContext(ctx,
		"SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?", name).Scan(&found)
	return err == nil && found == name
}

// blobTarget deletes one media blob. A blob that is already gone counts
// as confirmed, because deleting repeats safely.
type blobTarget struct {
	svc  *Service
	blob string
}

// Name identifies the target in progress reports.
func (t blobTarget) Name() string { return "blob/" + t.blob }

// Delete removes the blob. A refusal from the store fails the target,
// because only a missing blob reads as done.
func (t blobTarget) Delete(ctx context.Context) error {
	if err := t.svc.media.Delete(ctx, t.blob); err != nil {
		if errors.Is(err, mediastore.ErrNotFound) {
			return fmt.Errorf("privacy: erase blob %s: %w", t.blob, erase.ErrGone)
		}
		return fmt.Errorf("privacy: erase blob %s: %w", t.blob, err)
	}
	return nil
}

// coverTarget removes the cover file. The covers row cascades with the
// episode delete, so only the file needs its own target. A file that is
// already gone counts as confirmed.
type coverTarget struct {
	svc *Service
	ref eraseRef
}

// Name identifies the target in progress reports.
func (t coverTarget) Name() string { return "cover" }

// Delete removes the cover file for the erased episode.
func (t coverTarget) Delete(ctx context.Context) error {
	if err := os.Remove(t.svc.coverPath(t.ref.Episode)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("privacy: erase cover %s: %w", t.ref.Episode, erase.ErrGone)
		}
		return fmt.Errorf("privacy: erase cover %s: %w", t.ref.Episode, err)
	}
	return nil
}

// sessionTarget deletes the provider voice session. An already ended
// session reports success without a delete, which counts as confirmed.
type sessionTarget struct {
	svc *Service
	ref eraseRef
}

// Name identifies the target in progress reports.
func (t sessionTarget) Name() string { return "session" }

// Delete ends the provider session for the erased episode.
func (t sessionTarget) Delete(ctx context.Context) error {
	if _, err := t.svc.sessions.TerminateSession(ctx, t.ref.Session); err != nil {
		return fmt.Errorf("privacy: erase session: %w", err)
	}
	return nil
}

// transcriptTarget deletes one remaining provider batch transcript. It
// reads the copy first: a copy that already reads deleted needs no
// delete. Otherwise it deletes and confirms with a fresh fetch, the
// way the analysis pass confirms its own delete. A copy that stays
// readable fails the target, so the erasure names it stuck instead of
// reporting it erased.
type transcriptTarget struct {
	svc        *Service
	transcript string
}

// Name identifies the target in progress reports.
func (t transcriptTarget) Name() string { return "transcript/" + t.transcript }

// Delete removes one provider batch transcript and confirms it.
func (t transcriptTarget) Delete(ctx context.Context) error {
	first, err := t.svc.transcripts.Get(ctx, t.transcript)
	if err == nil && first.Deleted() {
		return nil
	}
	if err := t.svc.transcripts.Delete(ctx, t.transcript); err != nil {
		return fmt.Errorf("privacy: erase transcript %s: %w", t.transcript, err)
	}
	after, err := t.svc.transcripts.Get(ctx, t.transcript)
	if err != nil {
		return fmt.Errorf("privacy: erase transcript %s: confirm delete: %w", t.transcript, err)
	}
	if !after.Deleted() {
		return fmt.Errorf("privacy: erase transcript %s: %w", t.transcript, ErrDeleteUnconfirmed)
	}
	return nil
}
