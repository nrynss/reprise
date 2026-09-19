package retention_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nrynss/reprise/internal/retention"
)

// TestSweepDeletesExpiredKeepsFresh pins both halves of the sweep. The
// idle guest loses every content row, every media row and file, the user
// row, the session rows, and both provider copies. The fresh guest keeps
// every row and file by count.
func TestSweepDeletesExpiredKeepsFresh(t *testing.T) {
	fx := openFixture(t)
	idle := testNow.Add(-testWindow).Add(-time.Hour)
	fresh := testNow.Add(-time.Hour)
	gone := fx.seedGuest(t, "guest-gone", idle)
	stayed := fx.seedGuest(t, "guest-stayed", fresh)

	before := map[string]int{}
	for _, table := range contentTables {
		before[table] = fx.count(t, table, "guest-stayed")
	}
	beforeMedia := fx.mediaRows(t, "guest-stayed")

	jobID, err := fx.svc.Sweep(t.Context())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if jobID == "" {
		t.Fatalf("sweep returned an empty job id")
	}
	fx.waitSweepDone(t, jobID)

	for _, table := range contentTables {
		if got := fx.count(t, table, "guest-gone"); got != 0 {
			t.Fatalf("table %s holds %d idle guest rows, want none", table, got)
		}
	}
	var reconciled int
	for _, seed := range gone {
		if err := fx.db.Reader().QueryRowContext(t.Context(),
			"SELECT COUNT(*) FROM reconcile_state WHERE session_id = ?", "sess-"+seed.episode).Scan(&reconciled); err != nil {
			t.Fatalf("count reconcile rows: %v", err)
		}
		if reconciled != 0 {
			t.Fatalf("reconcile_state holds %d idle rows, want none", reconciled)
		}
	}
	if got := fx.mediaRows(t, "guest-gone"); got != 0 {
		t.Fatalf("media index holds %d idle rows, want none", got)
	}
	for _, seed := range gone {
		for _, blob := range append(append([]string{}, seed.stems...), seed.opus, seed.aac, seed.stereo) {
			if _, err := os.Stat(filepath.Join(fx.mediaDir, blob)); !os.IsNotExist(err) {
				t.Fatalf("blob file %s still on disk (err %v)", blob, err)
			}
		}
		if _, err := os.Stat(filepath.Join(fx.coverDir, seed.coverFile)); !os.IsNotExist(err) {
			t.Fatalf("cover file %s still on disk (err %v)", seed.coverFile, err)
		}
		if !fx.sessions.ended(seed.session) {
			t.Fatalf("provider session %s never ended", seed.session)
		}
		if !fx.transcript.deletedID(seed.batch) {
			t.Fatalf("provider transcript %s never deleted", seed.batch)
		}
	}
	if got := fx.count(t, "users", "guest-gone"); got != 0 {
		t.Fatalf("users holds %d idle rows, want none", got)
	}
	if got := fx.sessionRows(t, "guest-gone"); got != 0 {
		t.Fatalf("guest sessions holds %d idle rows, want none", got)
	}

	for _, table := range contentTables {
		if got := fx.count(t, table, "guest-stayed"); got != before[table] {
			t.Fatalf("table %s holds %d fresh rows, want %d", table, got, before[table])
		}
	}
	if got := fx.mediaRows(t, "guest-stayed"); got != beforeMedia {
		t.Fatalf("media index holds %d fresh rows, want %d", got, beforeMedia)
	}
	for _, seed := range stayed {
		for _, blob := range append(append([]string{}, seed.stems...), seed.opus, seed.aac, seed.stereo) {
			if _, err := os.Stat(filepath.Join(fx.mediaDir, blob)); err != nil {
				t.Fatalf("fresh blob %s missing (err %v)", blob, err)
			}
		}
		if fx.sessions.ended(seed.session) {
			t.Fatalf("fresh provider session ended, want it untouched")
		}
		if fx.transcript.deletedID(seed.batch) {
			t.Fatalf("fresh provider transcript deleted, want it untouched")
		}
	}
	if got := fx.count(t, "users", "guest-stayed"); got != 1 {
		t.Fatalf("users holds %d fresh rows, want 1", got)
	}
	if got := fx.sessionRows(t, "guest-stayed"); got != 1 {
		t.Fatalf("guest sessions holds %d fresh rows, want 1", got)
	}
}

// TestSweepBoundaryKeepsGuestAtCutoff pins the expiry edge. A guest seen
// exactly at the cutoff is not idle past the window, so the sweep keeps
// every row.
func TestSweepBoundaryKeepsGuestAtCutoff(t *testing.T) {
	fx := openFixture(t)
	edge := testNow.Add(-testWindow)
	fx.seedGuest(t, "guest-edge", edge)

	jobID, err := fx.svc.Sweep(t.Context())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	fx.waitSweepDone(t, jobID)

	if got := fx.count(t, "episodes", "guest-edge"); got != 2 {
		t.Fatalf("episodes holds %d edge rows, want 2", got)
	}
	if got := fx.count(t, "users", "guest-edge"); got != 1 {
		t.Fatalf("users holds %d edge rows, want 1", got)
	}
}

// TestSweepSkipsOwners pins that only guests expire. An idle owner keeps
// every row through the same run that sweeps an idle guest.
func TestSweepSkipsOwners(t *testing.T) {
	fx := openFixture(t)
	idle := testNow.Add(-testWindow).Add(-time.Hour)
	fx.seedUser(t, "owner-idle", "owner", idle)
	fx.seedEpisode(t, "owner-idle", 1)
	fx.seedGuest(t, "guest-gone", idle)

	jobID, err := fx.svc.Sweep(t.Context())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	fx.waitSweepDone(t, jobID)

	if got := fx.count(t, "episodes", "owner-idle"); got != 1 {
		t.Fatalf("episodes holds %d owner rows, want 1", got)
	}
	if got := fx.count(t, "users", "owner-idle"); got != 1 {
		t.Fatalf("users holds %d owner rows, want 1", got)
	}
	if got := fx.count(t, "episodes", "guest-gone"); got != 0 {
		t.Fatalf("episodes holds %d idle guest rows, want none", got)
	}
}

// TestSweepEmpty pins the no work run. With no expired guest the sweep
// still lands done and changes nothing.
func TestSweepEmpty(t *testing.T) {
	fx := openFixture(t)
	fx.seedGuest(t, "guest-fresh", testNow.Add(-time.Hour))

	jobID, err := fx.svc.Sweep(t.Context())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	fx.waitSweepDone(t, jobID)

	if got := fx.count(t, "episodes", "guest-fresh"); got != 2 {
		t.Fatalf("episodes holds %d rows, want 2", got)
	}
}

// TestSweepStuckFailsThenHeals pins the retry half. A provider outage
// fails the sweep naming the guest, and the next sweep after the outage
// finishes the same guest from its remaining rows.
func TestSweepStuckFailsThenHeals(t *testing.T) {
	fx := openFixture(t)
	idle := testNow.Add(-testWindow).Add(-time.Hour)
	gone := fx.seedGuest(t, "guest-gone", idle)
	fx.transcript.fail = true

	jobID, err := fx.svc.Sweep(t.Context())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		attempts, err := fx.jobStore.Attempts(t.Context(), jobID)
		if err != nil {
			t.Fatalf("list attempts: %v", err)
		}
		failed := false
		for _, rec := range attempts {
			if rec.Status == "error" {
				failed = true
			}
		}
		if failed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("stuck sweep never failed")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := fx.count(t, "episodes", "guest-gone"); got != 1 {
		t.Fatalf("episodes holds %d rows after a stuck sweep, want 1", got)
	}

	fx.transcript.fail = false
	next, err := fx.svc.ReSweep(t.Context(), jobID)
	if err != nil {
		t.Fatalf("re-sweep: %v", err)
	}
	fx.waitSweepDone(t, next)

	for _, seed := range gone {
		if !fx.transcript.deletedID(seed.batch) {
			t.Fatalf("provider transcript %s never deleted after heal", seed.batch)
		}
	}
	if got := fx.count(t, "users", "guest-gone"); got != 0 {
		t.Fatalf("users holds %d rows, want none", got)
	}
	if got := fx.sessionRows(t, "guest-gone"); got != 0 {
		t.Fatalf("guest sessions holds %d rows, want none", got)
	}
}

// TestDefaultWindowSweepsNinetyDayIdle pins the owner chosen default. A
// service with no window set sweeps a guest idle past ninety days and
// keeps a fresher one.
func TestDefaultWindowSweepsNinetyDayIdle(t *testing.T) {
	fx := openFixture(t)
	old := testNow.Add(-91 * 24 * time.Hour)
	young := testNow.Add(-89 * 24 * time.Hour)
	fx.seedGuest(t, "guest-old", old)
	fx.seedGuest(t, "guest-young", young)

	svc, err := retention.New(retention.Config{
		DB:          fx.db,
		Media:       fx.media,
		CoverDir:    fx.coverDir,
		Sessions:    fx.sessions,
		Transcripts: fx.transcript,
		Now:         func() time.Time { return testNow },
	})
	if err != nil {
		t.Fatalf("open default service: %v", err)
	}
	if err := svc.BindRunner(fx.runner); err != nil {
		t.Fatalf("bind runner: %v", err)
	}

	jobID, err := svc.Sweep(t.Context())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	fx.waitSweepDone(t, jobID)

	if got := fx.count(t, "episodes", "guest-old"); got != 0 {
		t.Fatalf("episodes holds %d old rows, want none", got)
	}
	if got := fx.count(t, "episodes", "guest-young"); got != 2 {
		t.Fatalf("episodes holds %d young rows, want 2", got)
	}
}

// TestReSweepCatchesNewlyExpired pins the retry cutoff. A guest who
// idles past the window while the sweep is down expires on the re-sweep,
// because every attempt reads expiry fresh and unions it with the
// unconfirmed prior guests. A guest still inside the window survives.
func TestReSweepCatchesNewlyExpired(t *testing.T) {
	fx := openFixture(t)
	fx.seedGuest(t, "guest-old", testNow.Add(-91*24*time.Hour))
	fx.seedGuest(t, "guest-mid", testNow.Add(-89*24*time.Hour))
	fx.seedGuest(t, "guest-fresh", testNow.Add(-time.Hour))
	fx.transcript.fail = true

	jobID, err := fx.svc.Sweep(t.Context())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		attempts, err := fx.jobStore.Attempts(t.Context(), jobID)
		if err != nil {
			t.Fatalf("list attempts: %v", err)
		}
		failed := false
		for _, rec := range attempts {
			if rec.Status == "error" {
				failed = true
			}
		}
		if failed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("stuck sweep never failed")
		}
		time.Sleep(20 * time.Millisecond)
	}
	fx.transcript.fail = false

	later, err := retention.New(retention.Config{
		DB:          fx.db,
		Media:       fx.media,
		CoverDir:    fx.coverDir,
		Sessions:    fx.sessions,
		Transcripts: fx.transcript,
		Window:      testWindow,
		Now:         func() time.Time { return testNow.Add(48 * time.Hour) },
	})
	if err != nil {
		t.Fatalf("open later service: %v", err)
	}
	if err := later.BindRunner(fx.runner); err != nil {
		t.Fatalf("bind runner: %v", err)
	}
	next, err := later.ReSweep(t.Context(), jobID)
	if err != nil {
		t.Fatalf("re-sweep: %v", err)
	}
	fx.waitSweepDone(t, next)

	if got := fx.count(t, "episodes", "guest-mid"); got != 0 {
		t.Fatalf("episodes holds %d newly-expired rows, want none", got)
	}
	if got := fx.count(t, "episodes", "guest-old"); got != 0 {
		t.Fatalf("episodes holds %d old rows, want none", got)
	}
	if got := fx.count(t, "episodes", "guest-fresh"); got != 2 {
		t.Fatalf("episodes holds %d fresh rows, want 2", got)
	}
}

// TestReSweepUnknown pins the retry fault. A re-sweep for an unknown job
// refuses instead of sweeping blind.
func TestReSweepUnknown(t *testing.T) {
	fx := openFixture(t)
	if _, err := fx.svc.ReSweep(t.Context(), "no-such-job"); err == nil {
		t.Fatalf("re-sweep succeeded for an unknown job, want an error")
	}
	if _, err := fx.svc.ReSweep(t.Context(), ""); !errors.Is(err, retention.ErrInvalid) {
		t.Fatalf("re-sweep err %v, want invalid config", err)
	}
}

// TestSweepWithoutRunner pins the wiring fault. A sweep with no runner
// bound refuses instead of running untracked deletes.
func TestSweepWithoutRunner(t *testing.T) {
	fx := openFixture(t)
	svc, err := retention.New(retention.Config{
		DB:          fx.db,
		Media:       fx.media,
		CoverDir:    fx.coverDir,
		Sessions:    fx.sessions,
		Transcripts: fx.transcript,
		Window:      testWindow,
		Now:         func() time.Time { return testNow },
	})
	if err != nil {
		t.Fatalf("open service: %v", err)
	}
	if _, err := svc.Sweep(t.Context()); !errors.Is(err, retention.ErrInvalid) {
		t.Fatalf("sweep err %v, want invalid config", err)
	}
}
