// Review probe for the finish chain. Copy this file into cmd/reprise/ of
// the reviewed worktree and run
//   go test -race -count=1 -run 'TestProbeMarkDoneWhileRenderBusyStillShips' ./cmd/reprise/
// It relies on the fixture helpers beside the binary tests.
package main

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/nrynss/keel/job"

	"github.com/nrynss/reprise/internal/episode"
)

// Guest A's render holds the one render slot, the way a second guest's
// render or a render the boot resumed or started does. Guest B marks
// done while it runs. B must not fail for good: it should wait in
// rendering, and render once the slot frees. Both episodes must ship.
func TestProbeMarkDoneWhileRenderBusyStillShips(t *testing.T) {
	fx := openWireFixture(t)
	useFinishModels(fx, false)
	j := bootFinishJobs(t, fx)
	routes, guests := finishRoutes(t, fx, j)
	cookieA, ownerA := finishGuest(t, guests)
	cookieB, ownerB := finishGuest(t, guests)
	first := finishDraft(t, fx, ownerA)
	second := finishDraft(t, fx, ownerB)

	release := make(chan struct{})
	var once sync.Once
	free := func() { once.Do(func() { close(release) }) }
	t.Cleanup(free)
	if err := episode.MarkDone(t.Context(), fx.db, first); err != nil {
		t.Fatalf("mark first done: %v", err)
	}
	held := func(ctx context.Context, progress func(job.Progress)) ([]byte, error) {
		<-release
		return j.renderFunc(ownerA, first)(ctx, progress)
	}
	if _, err := j.StartEpisodeKind(t.Context(), kindRender, ownerA, first, held); err != nil {
		t.Fatalf("hold the render slot: %v", err)
	}
	_ = cookieA

	code := postDone(t, routes, cookieB, second)
	if got := finishState(t, fx, second); got == episode.StateFailed {
		t.Fatalf("mark done while the render slot was busy answered %d and left the episode %s; "+
			"no route moves a failed episode back, so it never ships", code, got)
	}
	if code != http.StatusAccepted {
		t.Errorf("mark done while the render slot was busy answered %d, want %d", code, http.StatusAccepted)
	}

	free()
	deadline := time.Now().Add(2 * time.Minute)
	for {
		j.advanceAll(t.Context())
		if finishState(t, fx, first) == episode.StateReady && finishState(t, fx, second) == episode.StateReady {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("first %s, second %s, want both ready", finishState(t, fx, first), finishState(t, fx, second))
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, id := range []string{first, second} {
		if n := kindJobsFor(t, fx, kindRender, id); n != 1 {
			t.Fatalf("render jobs for %s = %d, want one", id, n)
		}
	}
}
