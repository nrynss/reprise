package retention_test

import (
	"errors"
	"testing"
	"time"

	"github.com/nrynss/keel/job"
	"github.com/nrynss/keel/stream"
)

// TestSweepRestartResumesAndFinishes pins the crash half. The provider
// delete holds mid-sweep while a fresh runner opens on the same job
// ledger, the way a restarted process reopens it. The test then stops
// the superseded attempt before releasing the provider, the way the
// crashed process stays dead. The new runner resumes from its snapshot,
// so the sweep finishes with the expired guest gone and the fresh
// guest intact.
func TestSweepRestartResumesAndFinishes(t *testing.T) {
	fx := openFixture(t)
	idle := testNow.Add(-testWindow).Add(-time.Hour)
	gone := fx.seedGuest(t, "guest-gone", idle)
	stayed := fx.seedGuest(t, "guest-fresh", testNow.Add(-time.Hour))

	release := make(chan struct{})
	fx.sessions.block = release

	jobID, err := fx.svc.Sweep(t.Context())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	fx.waitGuestEpisodesBelow(t, "guest-gone", 2)

	old := fx.runner
	runner, err := job.Open(t.Context(), job.Config{
		Broker: stream.New(stream.Config{}),
		Store:  fx.jobStore,
		Kinds:  fx.svc.Kinds(),
	})
	if err != nil {
		t.Fatalf("reopen runner: %v", err)
	}
	if err := fx.svc.BindRunner(runner); err != nil {
		t.Fatalf("bind reopened runner: %v", err)
	}
	fx.runner = runner
	if err := old.Cancel(jobID); err != nil && !errors.Is(err, job.ErrUnknownJob) {
		t.Fatalf("stop superseded attempt: %v", err)
	}
	fx.waitFirstTerminal(t, jobID)
	close(release)

	fx.waitSweepDone(t, jobID)

	for _, table := range contentTables {
		if got := fx.count(t, table, "guest-gone"); got != 0 {
			t.Fatalf("table %s holds %d idle rows, want none", table, got)
		}
	}
	if got := fx.mediaRows(t, "guest-gone"); got != 0 {
		t.Fatalf("media index holds %d idle rows, want none", got)
	}
	if got := fx.count(t, "users", "guest-gone"); got != 0 {
		t.Fatalf("users holds %d idle rows, want none", got)
	}
	for _, seed := range gone {
		if !fx.sessions.ended(seed.session) {
			t.Fatalf("provider session %s never ended across the restart", seed.session)
		}
		if !fx.transcript.deletedID(seed.batch) {
			t.Fatalf("provider transcript %s never deleted across the restart", seed.batch)
		}
	}
	for _, seed := range stayed {
		if fx.sessions.ended(seed.session) {
			t.Fatalf("fresh provider session ended, want it untouched")
		}
	}
	if got := fx.count(t, "episodes", "guest-fresh"); got != 2 {
		t.Fatalf("episodes holds %d fresh rows, want 2", got)
	}
}
