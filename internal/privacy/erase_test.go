package privacy_test

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/nrynss/keel/job"
	"github.com/nrynss/reprise/internal/privacy"
)

// contentTables lists every diary table that must empty for an erased
// owner. The owner row itself survives, the way account deletion never
// rides along with episode deletion.
var contentTables = []string{
	"episodes", "sessions", "stems", "turns", "words", "proposals",
	"decisions", "renders", "analyses", "mentions", "callbacks",
}

// TestEraseRemovesEverything pins the erase half. After the job lands,
// the database holds no content row for the erased owner, the media
// directory holds no file of hers, the provider doubles recorded
// every delete, and the second owner stands untouched row by row.
func TestEraseRemovesEverything(t *testing.T) {
	fx := openFixture(t)
	fx.as("owner-a")
	gone := fx.seeds["owner-a"]
	stayed := fx.seeds["owner-b"]

	jobID, err := fx.svc.Erase(t.Context(), gone.episode)
	if err != nil {
		t.Fatalf("erase: %v", err)
	}
	if jobID == "" {
		t.Fatalf("erase returned an empty job id")
	}
	fx.waitJobDone(t, jobID)

	for _, table := range contentTables {
		if got := fx.count(t, table, "owner-a"); got != 0 {
			t.Fatalf("table %s holds %d owner-a rows, want none", table, got)
		}
	}
	var reconciled int
	if err := fx.db.Reader().QueryRowContext(t.Context(),
		"SELECT COUNT(*) FROM reconcile_state WHERE session_id = ?", "sess-owner-a").Scan(&reconciled); err != nil {
		t.Fatalf("count reconcile rows: %v", err)
	}
	if reconciled != 0 {
		t.Fatalf("reconcile_state holds %d owner-a rows, want none", reconciled)
	}
	if got := fx.count(t, "users", "owner-a"); got != 1 {
		t.Fatalf("users holds %d owner-a rows, want the owner to survive", got)
	}

	var mediaRows int
	if err := fx.db.Reader().QueryRowContext(t.Context(),
		"SELECT COUNT(*) FROM media WHERE owner = ?", "owner-a").Scan(&mediaRows); err != nil {
		t.Fatalf("count media rows: %v", err)
	}
	if mediaRows != 0 {
		t.Fatalf("media index holds %d owner-a rows, want none", mediaRows)
	}
	for _, blob := range append(append([]string{}, gone.stems...), gone.opus, gone.aac, gone.stereo) {
		if _, err := os.Stat(filepath.Join(fx.mediaDir, blob)); !os.IsNotExist(err) {
			t.Fatalf("blob file %s still on disk (err %v)", blob, err)
		}
		if got := signedOutMedia(fx, t, blob).Code; got != http.StatusNotFound {
			t.Fatalf("erased blob %s status %d, want 404", blob, got)
		}
	}
	if _, err := os.Stat(filepath.Join(fx.coverDir, gone.coverFile)); !os.IsNotExist(err) {
		t.Fatalf("cover file still on disk (err %v)", err)
	}

	if !fx.sessions.ended(gone.session) {
		t.Fatalf("provider session %s never ended", gone.session)
	}
	found := false
	for _, call := range fx.transcript.deletes {
		if call == gone.batch {
			found = true
		}
	}
	if !found {
		t.Fatalf("provider transcript %s never deleted", gone.batch)
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
	for _, blob := range append(append([]string{}, stayed.stems...), stayed.opus, stayed.aac, stayed.stereo) {
		if _, err := os.Stat(filepath.Join(fx.mediaDir, blob)); err != nil {
			t.Fatalf("owner-b blob %s missing (err %v)", blob, err)
		}
	}
	if fx.sessions.ended(stayed.session) {
		t.Fatalf("second owner session ended, want it untouched")
	}
	rep, err := fx.svc.Eraser().Inspect(t.Context(), fx.runner, jobID)
	if err != nil {
		t.Fatalf("inspect erasure: %v", err)
	}
	if !rep.Complete() {
		t.Fatalf("erasure still owes %v", rep.Stuck)
	}
}

// TestEraseStuckNamesProviderTargets pins the failure half. A provider
// outage fails the job with the local deletes done and the provider
// deletes owed by name. A retry after the outage finishes the erasure
// from the recorded ref, even though the episode row is already gone.
func TestEraseStuckNamesProviderTargets(t *testing.T) {
	fx := openFixture(t)
	fx.as("owner-a")
	gone := fx.seeds["owner-a"]
	fx.transcript.fail = true

	jobID, err := fx.svc.Erase(t.Context(), gone.episode)
	if err != nil {
		t.Fatalf("erase: %v", err)
	}
	deadline := waitDeadline()
	for {
		attempts, err := fx.jobStore.Attempts(t.Context(), jobID)
		if err != nil {
			t.Fatalf("list attempts: %v", err)
		}
		failed := false
		for _, rec := range attempts {
			if rec.Status == job.StatusError {
				failed = true
			}
		}
		if failed {
			break
		}
		pastDeadline(t, deadline, "stuck job never failed")
	}
	rep, err := fx.svc.Eraser().Inspect(t.Context(), fx.runner, jobID)
	if err != nil {
		t.Fatalf("inspect erasure: %v", err)
	}
	if rep.Complete() {
		t.Fatalf("erasure reads complete past a provider outage")
	}
	if len(rep.Stuck) != 1 || rep.Stuck[0] != "transcript/"+gone.batch {
		t.Fatalf("stuck %v, want only the batch transcript", rep.Stuck)
	}
	if got := fx.count(t, "episodes", "owner-a"); got != 0 {
		t.Fatalf("episode row survived a stuck erasure, want none")
	}

	fx.transcript.fail = false
	fx.as("owner-b")
	if _, err := fx.svc.ReErase(t.Context(), jobID); !errors.Is(err, privacy.ErrNotOwner) {
		t.Fatalf("foreign re-erase err %v, want not owner", err)
	}
	fx.as("owner-a")
	next, err := fx.svc.ReErase(t.Context(), jobID)
	if err != nil {
		t.Fatalf("re-erase: %v", err)
	}
	fx.waitJobDone(t, next)
	rep, err = fx.svc.Eraser().Inspect(t.Context(), fx.runner, next)
	if err != nil {
		t.Fatalf("inspect retry: %v", err)
	}
	if !rep.Complete() {
		t.Fatalf("retry still owes %v", rep.Stuck)
	}
	if _, err := fx.svc.ReErase(t.Context(), next); !errors.Is(err, privacy.ErrComplete) {
		t.Fatalf("finished re-erase err %v, want complete", err)
	}
}
