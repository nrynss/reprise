package privacy_test

import (
	"errors"
	"testing"

	"github.com/nrynss/keel/erase"
	"github.com/nrynss/keel/job"
	"github.com/nrynss/keel/stream"
)

// TestEraseRestartResumesAndFinishes pins the crash half. The provider
// delete holds mid-erase while a fresh runner opens on the same job
// ledger, the way a restarted process reopens it. The test then stops
// the superseded attempt before releasing the provider, the way the
// crashed process stays dead. The new runner resumes from its ledger,
// so only the unconfirmed targets delete again and the erasure
// finishes exactly once.
func TestEraseRestartResumesAndFinishes(t *testing.T) {
	fx := openFixture(t)
	fx.as("owner-a")
	gone := fx.seeds["owner-a"]
	stayed := fx.seeds["owner-b"]

	release := make(chan struct{})
	fx.sessions.block = release

	jobID, err := fx.svc.Erase(t.Context(), gone.episode)
	if err != nil {
		t.Fatalf("erase: %v", err)
	}
	fx.waitErased(t, gone.episode)

	old := fx.runner
	runner, err := job.Open(t.Context(), job.Config{
		Broker: stream.New(stream.Config{}),
		Store:  fx.jobStore,
		Kinds:  map[string]job.Kind{erase.KindName: fx.svc.Eraser().Kind()},
	})
	if err != nil {
		t.Fatalf("reopen runner: %v", err)
	}
	if err := fx.svc.BindRunner(runner); err != nil {
		t.Fatalf("bind reopened runner: %v", err)
	}
	if err := old.Cancel(jobID); err != nil && !errors.Is(err, job.ErrUnknownJob) {
		t.Fatalf("stop superseded attempt: %v", err)
	}
	fx.waitFirstTerminal(t, jobID)
	close(release)

	fx.waitJobDone(t, jobID)
	rep, err := fx.svc.Eraser().Inspect(t.Context(), runner, jobID)
	if err != nil {
		t.Fatalf("inspect resumed erasure: %v", err)
	}
	if !rep.Complete() {
		t.Fatalf("resumed erasure still owes %v", rep.Stuck)
	}
	if !fx.sessions.ended(gone.session) {
		t.Fatalf("provider session %s never ended across the restart", gone.session)
	}
	if fx.sessions.ended(stayed.session) {
		t.Fatalf("second owner session ended, want it untouched")
	}
	if got := fx.count(t, "episodes", "owner-b"); got != 1 {
		t.Fatalf("second owner episode count %d, want 1", got)
	}
}
