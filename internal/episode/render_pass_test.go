package episode_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/nrynss/keel/job"
	jobsqlitestore "github.com/nrynss/keel/job/sqlitestore"
	"github.com/nrynss/reprise/internal/episode"
)

// seedRenderPassRow stores one job record stamped for an episode and a
// render, and fails the test on error.
func seedRenderPassRow(t *testing.T, store *jobsqlitestore.Store, id, kind, episodeID, renderID string, status job.Status, updatedAt time.Time) {
	t.Helper()
	detail, err := json.Marshal(map[string]string{"owner_id": "owner-1", "episode_id": episodeID, "render_id": renderID})
	if err != nil {
		t.Fatalf("encode linkage: %v", err)
	}
	rec := job.Record{
		ID: id, Kind: kind, Status: status, Attempt: 1, RootID: id,
		Progress:  job.Progress{Stage: "start", Detail: detail},
		UpdatedAt: updatedAt,
	}
	if err := store.Create(t.Context(), rec); err != nil {
		t.Fatalf("create job %s: %v", id, err)
	}
}

// TestLastRenderKindJobKeepsToOneRender stores an analysis pass on an
// older render and none on the newer one. The render scoped read must
// find the older pass for the older render only, so a newer render
// always gets its own pass.
func TestLastRenderKindJobKeepsToOneRender(t *testing.T) {
	t.Parallel()
	db := openDatabase(t)
	store := openJobStore(t, db)
	plantEpisode(t, db, "ep-1", 1, episode.StateAnalysing)
	seedRenderPassRow(t, store, "job-old", episode.AnalysisKind, "ep-1", "render-old", job.StatusDone, time.Now())

	old, err := episode.LastRenderKindJob(t.Context(), db, "ep-1", "render-old", episode.AnalysisKind)
	if err != nil {
		t.Fatalf("old render pass: %v", err)
	}
	if !old.Found || old.JobID != "job-old" {
		t.Fatalf("old render pass = %+v, want job-old", old)
	}
	fresh, err := episode.LastRenderKindJob(t.Context(), db, "ep-1", "render-new", episode.AnalysisKind)
	if err != nil {
		t.Fatalf("new render pass: %v", err)
	}
	if fresh.Found {
		t.Fatalf("new render pass = %+v, want none", fresh)
	}
	latest, err := episode.LastKindJob(t.Context(), db, "ep-1", episode.AnalysisKind)
	if err != nil {
		t.Fatalf("episode pass: %v", err)
	}
	if !latest.Found || latest.JobID != "job-old" {
		t.Fatalf("episode pass = %+v, want job-old", latest)
	}
	if _, err := episode.LastRenderKindJob(t.Context(), db, "ep-1", "", episode.AnalysisKind); !errors.Is(err, episode.ErrInvalid) {
		t.Fatalf("empty render error = %v, want ErrInvalid", err)
	}
}

// TestRenderPassOutcomesNameEachPass stores a done render, a failed
// analysis, and a running marking pass. Each outcome names its own pass,
// a foreign owner gets not found, and a service with no render kind
// reports none.
func TestRenderPassOutcomesNameEachPass(t *testing.T) {
	t.Parallel()
	db := openDatabase(t)
	store := openJobStore(t, db)
	plantEpisode(t, db, "ep-1", 1, episode.StateAnalysing)
	now := time.Now()
	seedJobRow(t, store, "job-render", "render", "owner-1", "ep-1", job.StatusDone, "", now.Add(-2*time.Minute))
	seedRenderPassRow(t, store, "job-analysis", episode.AnalysisKind, "ep-1", "render-1", job.StatusError, now.Add(-time.Minute))
	seedRenderPassRow(t, store, "job-memory", episode.MemoryKind, "ep-1", "render-1", job.StatusRunning, now)
	svc := mustService(t, db, &fakeStarter{id: "job-1"})

	cases := []struct {
		name string
		read func(ctx context.Context, owner, id string) (episode.Outcome, error)
		want string
	}{
		{name: "render", read: svc.RenderOutcome, want: "job-render"},
		{name: "analysis", read: svc.AnalysisOutcome, want: "job-analysis"},
		{name: "memory", read: svc.MemoryOutcome, want: "job-memory"},
	}
	for _, tc := range cases {
		got, err := tc.read(t.Context(), "owner-1", "ep-1")
		if err != nil {
			t.Fatalf("%s outcome: %v", tc.name, err)
		}
		if !got.Found || got.JobID != tc.want {
			t.Fatalf("%s outcome = %+v, want %s", tc.name, got, tc.want)
		}
		if _, err := tc.read(t.Context(), "owner-2", "ep-1"); !errors.Is(err, episode.ErrNotFound) {
			t.Fatalf("%s foreign owner err = %v, want ErrNotFound", tc.name, err)
		}
	}
	bare, err := episode.NewService(episode.Config{DB: db})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	for _, read := range []func(context.Context, string, string) (episode.Outcome, error){
		bare.RenderOutcome, bare.AnalysisOutcome, bare.MemoryOutcome,
	} {
		got, err := read(t.Context(), "owner-1", "ep-1")
		if err != nil {
			t.Fatalf("bare outcome: %v", err)
		}
		if got.Found {
			t.Fatalf("bare outcome = %+v, want none with no render kind", got)
		}
	}
}

// stampingStarter records the episode each start names, so the test sees
// mark done hand the episode to the starter.
type stampingStarter struct {
	fakeStarter
	stamped []string
}

// StartEpisodeKind records the episode and starts like the plain starter.
func (s *stampingStarter) StartEpisodeKind(ctx context.Context, kind, ownerID, episodeID string, fn job.Func) (string, error) {
	s.stamped = append(s.stamped, ownerID+"/"+episodeID)
	return s.StartKind(ctx, kind, fn)
}

// TestRequestRenderNamesTheEpisode requires mark done to start the render
// through the episode seam when the starter offers it, so the render job
// carries its episode from the start.
func TestRequestRenderNamesTheEpisode(t *testing.T) {
	t.Parallel()
	db := openDatabase(t)
	plantEpisode(t, db, "ep-1", 1, episode.StateDraft)
	starter := &stampingStarter{fakeStarter: fakeStarter{id: "job-1"}}
	svc, err := episode.NewService(episode.Config{
		DB: db, Starter: starter, RenderKind: "render", Render: fakeRender{},
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	jobID, err := svc.RequestRender(t.Context(), "owner-1", "ep-1")
	if err != nil {
		t.Fatalf("request render: %v", err)
	}
	if jobID != "job-1" || starter.calls != 1 || starter.kind != "render" {
		t.Fatalf("job = %q calls = %d kind = %q, want one render start", jobID, starter.calls, starter.kind)
	}
	if len(starter.stamped) != 1 || starter.stamped[0] != "owner-1/ep-1" {
		t.Fatalf("stamped = %v, want the one episode", starter.stamped)
	}
}
