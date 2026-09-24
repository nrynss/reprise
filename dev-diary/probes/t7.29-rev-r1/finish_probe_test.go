// Review probes for the finish chain. Copy this file into cmd/reprise/ of
// the reviewed worktree and run
//   go test -race -run 'TestProbe' ./cmd/reprise/
// It relies on the fixture helpers beside the binary tests.
package main

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/nrynss/keel/job"

	"github.com/nrynss/reprise/internal/analysis"
	"github.com/nrynss/reprise/internal/assemblyai"
	"github.com/nrynss/reprise/internal/episode"
	"github.com/nrynss/reprise/internal/render"
	"github.com/nrynss/reprise/internal/retention"
)

// Two episodes a restart left rendering with no render job behind them.
// The render kind runs one job at a time, so the boot starts one render.
// The second episode must still leave rendering, but nothing retries it.
func TestProbeRecoverRendersEveryStuckEpisode(t *testing.T) {
	fx := openWireFixture(t)
	useFinishModels(fx, false)
	const owner = "owner-probe-stuck"
	insertWireUser(t, fx, owner)
	first := finishDraft(t, fx, owner)
	second := finishDraft(t, fx, owner)
	for _, id := range []string{first, second} {
		if _, err := fx.db.Writer().ExecContext(t.Context(),
			`UPDATE episodes SET state = ? WHERE id = ?`, string(episode.StateRendering), id); err != nil {
			t.Fatalf("plant rendering: %v", err)
		}
	}
	j := bootFinishJobs(t, fx)
	waitState(t, fx, first, episode.StateReady)
	// The advance loop is the only retry the binary runs. Tick it by hand.
	j.advanceAll(t.Context())
	waitIdle(t, fx)
	if got := finishState(t, fx, second); got == episode.StateRendering {
		t.Fatalf("second stuck episode still %s with %d render jobs, nothing will ever render it",
			got, kindJobsFor(t, fx, kindRender, second))
	}
}

// After a full chain, the settled map should hold nothing for an episode
// that reached ready.
func TestProbeSettledMapDrains(t *testing.T) {
	fx := openWireFixture(t)
	useFinishModels(fx, false)
	j := bootFinishJobs(t, fx)
	const owner = "owner-probe-settled"
	insertWireUser(t, fx, owner)
	episodeID := finishDraft(t, fx, owner)
	svc, err := episode.NewService(episodeConfig(fx.db, j))
	if err != nil {
		t.Fatalf("service: %v", err)
	}
	if _, err := svc.RequestRender(t.Context(), owner, episodeID); err != nil {
		t.Fatalf("request render: %v", err)
	}
	waitState(t, fx, episodeID, episode.StateReady)
	j.advanceAll(t.Context())
	j.schedMu.Lock()
	n := len(j.settled)
	j.schedMu.Unlock()
	if n != 0 {
		t.Fatalf("settled holds %d entries after the episode shipped", n)
	}
}

type probeSessions struct{}

func (probeSessions) TerminateSession(context.Context, string) (assemblyai.TerminateResult, error) {
	return assemblyai.TerminateResult{}, nil
}

type probeTranscripts struct{}

func (probeTranscripts) Delete(context.Context, string) error { return nil }
func (probeTranscripts) Get(context.Context, string) (assemblyai.Transcript, error) {
	return assemblyai.Transcript{}, nil
}

// Keep moves a guest episode to the owner and promises no row still names
// the guest. The render link row keeps the guest as owner, so the guest
// row can no longer be deleted. The fixture holds no guest session rows,
// so the probe runs the sweep's user delete alone.
func TestProbeKeepLeavesRenderLinkOnTheGuest(t *testing.T) {
	fx := openWireFixture(t)
	const guest = "guest-probe-keep"
	const keeper = "keeper-probe-keep"
	insertWireUser(t, fx, guest)
	insertWireUser(t, fx, keeper)
	episodeID := draftEpisode(t, fx, guest)
	if _, err := fx.db.Writer().ExecContext(t.Context(),
		`INSERT INTO renders (id, owner_id, episode_id, input_hash, opus_media_id, aac_media_id, loudness)
		 VALUES ('render-keep', ?, ?, 'hash', 'opus', 'aac', -16)`, guest, episodeID); err != nil {
		t.Fatalf("seed render: %v", err)
	}
	if err := analysis.ReplaceWords(t.Context(), fx.db.Writer(), guest, episodeID, "render-keep",
		[]analysis.Word{{Text: "hi", StartMs: 0, EndMs: 100}}); err != nil {
		t.Fatalf("replace words: %v", err)
	}
	svc, err := retention.New(retention.Config{
		DB: fx.db, Media: fx.media, CoverDir: t.TempDir(),
		Sessions: probeSessions{}, Transcripts: probeTranscripts{}, Window: time.Hour,
	})
	if err != nil {
		t.Fatalf("retention: %v", err)
	}
	if err := svc.Keep(t.Context(), episodeID, keeper); err != nil {
		t.Fatalf("keep: %v", err)
	}
	var owner string
	if err := fx.db.Reader().QueryRowContext(t.Context(),
		`SELECT owner_id FROM rendered_sources WHERE episode_id = ?`, episodeID).Scan(&owner); err != nil {
		t.Fatalf("read link: %v", err)
	}
	if owner != keeper {
		t.Errorf("render link owner = %q after keep, want %q", owner, keeper)
	}
	if _, err := fx.db.Writer().ExecContext(t.Context(),
		`DELETE FROM users WHERE id = ? AND kind = 'guest'`, guest); err != nil {
		t.Errorf("sweep delete of the guest row after keep: %v", err)
	}
}

// Outside the reviewed task. Keep leaves the cover row on the guest too,
// so the same sweep delete fails. The owner of internal/retention fixes it.
func TestProbeKeepLeavesCoverOnTheGuest(t *testing.T) {
	fx := openWireFixture(t)
	const guest = "guest-probe-cover"
	const keeper = "keeper-probe-cover"
	insertWireUser(t, fx, guest)
	insertWireUser(t, fx, keeper)
	episodeID := draftEpisode(t, fx, guest)
	j := &jobs{pipe: fx.pipe}
	if err := j.plainCover(t.Context(), guest, episodeID); err != nil {
		t.Fatalf("plain cover: %v", err)
	}
	svc, err := retention.New(retention.Config{
		DB: fx.db, Media: fx.media, CoverDir: t.TempDir(),
		Sessions: probeSessions{}, Transcripts: probeTranscripts{}, Window: time.Hour,
	})
	if err != nil {
		t.Fatalf("retention: %v", err)
	}
	if err := svc.Keep(t.Context(), episodeID, keeper); err != nil {
		t.Fatalf("keep: %v", err)
	}
	if _, err := fx.db.Writer().ExecContext(t.Context(),
		`DELETE FROM users WHERE id = ? AND kind = 'guest'`, guest); err != nil {
		t.Errorf("sweep delete of the guest row after keep: %v", err)
	}
}

// Two completions of one render race. One chain starts, so one analysis,
// one cover and one marking pass run.
func TestProbeRacingRenderCompletionsStartOneChain(t *testing.T) {
	fx := openWireFixture(t)
	scripted := useFinishModels(fx, false)
	j := bootFinishJobs(t, fx)
	const owner = "owner-probe-race"
	insertWireUser(t, fx, owner)
	episodeID := finishDraft(t, fx, owner)
	if _, err := fx.db.Writer().ExecContext(t.Context(),
		`UPDATE episodes SET state = ? WHERE id = ?`, string(episode.StateRendering), episodeID); err != nil {
		t.Fatalf("plant rendering: %v", err)
	}
	out, err := j.resolver.Func(owner, episodeID)(t.Context(), func(job.Progress) {})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			if _, err := j.settleRender(t.Context(), owner, episodeID, out, nil); err != nil {
				t.Errorf("settle: %v", err)
			}
		})
	}
	wg.Wait()
	waitState(t, fx, episodeID, episode.StateReady)
	for _, kind := range []string{kindAnalysis, kindCover, kindMemory} {
		if n := kindJobsFor(t, fx, kind, episodeID); n != 1 {
			t.Fatalf("%s jobs = %d, want one", kind, n)
		}
	}
	if scripted.Creates != 1 {
		t.Fatalf("batch creates = %d, want one", scripted.Creates)
	}
}

// Outside the reviewed task. The render package declares its kind
// resumable, but with no attempt cap above one the runner never resumes
// it. The owner of internal/render fixes it.
func TestProbeRenderKindResumes(t *testing.T) {
	if got := render.KindOf(&render.Resolver{}).MaxAttempts; got < 2 {
		t.Fatalf("render.KindOf MaxAttempts = %d, so a restart never resumes a render", got)
	}
}
