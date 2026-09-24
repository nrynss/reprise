package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/nrynss/keel/cost"
	"github.com/nrynss/keel/job"
	"github.com/nrynss/reprise/internal/cover"
	"github.com/nrynss/reprise/internal/episode"
	"github.com/nrynss/reprise/internal/render"
)

// The finish chain lives here. Mark done starts one render job. A render
// that finishes moves the episode to analysing and starts the analysis
// and cover passes. A finished analysis starts the marking pass. The
// episode reads ready once analysis and marking both stop. Each pass
// after the render stamps the render it works on, so every start checks
// for a pass on that render first and a repeat starts nothing.

// renderAttempts caps the render attempts a restart may make. The render
// calls nothing paid and names its output by its inputs, so a resumed
// attempt lands on the same file. The cap still stops a render that dies
// every time from resuming forever.
const renderAttempts = 3

// advanceInterval spaces the passes that start waiting finish work. A
// kind at its limit refuses a start, and the next tick starts it once a
// slot frees. That covers a render as well as the passes after it.
const advanceInterval = 30 * time.Second

// errNoRunner reports a start before the runner opened, or after it
// failed to open.
var errNoRunner = errors.New("reprise: job runner is not open")

// errPlainCover is the answer the plain cover model gives every call, so
// the cover pass stores its deterministic panel.
var errPlainCover = errors.New("reprise: plain cover draws no art")

// renderChain adapts the scheduler to the render seam the episode service
// declares. Mark done builds the render work through it, so the render
// the service starts is the chained one.
type renderChain struct {
	jobs *jobs
}

// Func returns the chained render work for one episode.
func (c renderChain) Func(ownerID, episodeID string) job.Func {
	return c.jobs.renderFunc(ownerID, episodeID)
}

// jobRunner returns the runner once it opened. Work the runner resumes
// during its own open waits here, so it never reads the runner before the
// open finished. A scheduler built with its runner in place never waits.
func (j *jobs) jobRunner() *job.Runner {
	if j.ready != nil {
		<-j.ready
	}
	return j.runner
}

// StartKind runs fn as a job of the named kind.
func (j *jobs) StartKind(ctx context.Context, kind string, fn job.Func) (string, error) {
	runner := j.jobRunner()
	if runner == nil {
		return "", errNoRunner
	}
	return runner.StartKind(ctx, kind, fn)
}

// StartEpisodeKind runs fn as a job of the named kind and stamps the
// episode on it before returning, so the detail names the job from its
// first read. A render start answers with the render already running
// for the episode, if one is, so mark done never races the advance into
// a second render.
func (j *jobs) StartEpisodeKind(ctx context.Context, kind, ownerID, episodeID string, fn job.Func) (string, error) {
	if kind == kindRender {
		j.schedMu.Lock()
		defer j.schedMu.Unlock()
		last, err := episode.LastKindJob(ctx, j.pipe.db, episodeID, kindRender)
		if err != nil {
			return "", err
		}
		if last.Found && !jobTerminal(last.Status) {
			return last.JobID, nil
		}
	}
	return j.startStamped(ctx, kind, episodeDescriptor{OwnerID: ownerID, EpisodeID: episodeID}, fn)
}

// startStamped starts one job and stamps its linkage before returning. A
// stamp failure cancels the job and reports the error, so no silent job
// keeps running.
func (j *jobs) startStamped(ctx context.Context, kind string, desc episodeDescriptor, fn job.Func) (string, error) {
	runner := j.jobRunner()
	if runner == nil {
		return "", errNoRunner
	}
	jobID, err := runner.StartKind(ctx, kind, fn)
	if err != nil {
		return "", err
	}
	if err := stampJob(ctx, j.store, jobID, desc); err != nil {
		_ = runner.Cancel(jobID)
		return "", err
	}
	return jobID, nil
}

// withLinkage stamps every progress report with the job linkage. A report
// replaces the stored snapshot whole, so a later stage report would
// otherwise drop the linkage the restart pass and the detail read.
func withLinkage(progress func(job.Progress), desc episodeDescriptor) func(job.Progress) {
	raw, err := json.Marshal(desc)
	if err != nil || progress == nil {
		return progress
	}
	return func(p job.Progress) {
		p.Detail = raw
		progress(p)
	}
}

// renderKind registers the render with the chained resume. The render
// package sets the limit and marks it idempotent. The resume rebuilds the
// chained work, so a resumed render still moves its episode on.
func (j *jobs) renderKind() job.Kind {
	kind := render.KindOf(j.resolver)
	kind.MaxAttempts = renderAttempts
	kind.Resume = j.resumeRender
	return kind
}

// resumeRender rebuilds the chained render work from the linkage the
// interrupted attempt carried.
func (j *jobs) resumeRender(rec job.Record) (job.Func, error) {
	var desc episodeDescriptor
	if err := json.Unmarshal(rec.Progress.Detail, &desc); err != nil {
		return nil, fmt.Errorf("reprise: resume render: %w", err)
	}
	if desc.OwnerID == "" || desc.EpisodeID == "" {
		return nil, fmt.Errorf("reprise: resume render %s: record carries no episode", rec.ID)
	}
	return j.renderFunc(desc.OwnerID, desc.EpisodeID), nil
}

// renderFunc builds the render work for one episode, chained to the
// settlement that moves the episode on.
func (j *jobs) renderFunc(ownerID, episodeID string) job.Func {
	return func(ctx context.Context, progress func(job.Progress)) ([]byte, error) {
		inner := j.resolver.Func(ownerID, episodeID)
		out, err := inner(ctx, withLinkage(progress, episodeDescriptor{OwnerID: ownerID, EpisodeID: episodeID}))
		return j.settleRender(ctx, ownerID, episodeID, out, err)
	}
}

// settleRender records one finished render for its episode. A failed
// render fails a rendering episode, so the card names the render. A
// render that finishes on an episode already past rendering starts
// nothing, so a repeated completion never starts a second chain.
// Otherwise the finished render becomes the newest render row, the
// episode moves to analysing, and the passes after it start.
func (j *jobs) settleRender(ctx context.Context, ownerID, episodeID string, out []byte, runErr error) ([]byte, error) {
	db := j.pipe.db
	if runErr != nil {
		if err := episode.Transition(context.WithoutCancel(ctx), db, episodeID, episode.StateRendering, episode.StateFailed); err != nil &&
			!errors.Is(err, episode.ErrIllegalTransition) {
			log.Printf("reprise finish: fail episode %s: %v", episodeID, err)
		}
		return nil, runErr
	}
	var res render.Result
	if err := json.Unmarshal(out, &res); err != nil {
		return nil, fmt.Errorf("reprise: decode render result: %w", err)
	}
	moved, err := j.markRendered(ctx, ownerID, episodeID, res)
	if err != nil {
		if ferr := episode.Transition(context.WithoutCancel(ctx), db, episodeID, episode.StateRendering, episode.StateFailed); ferr != nil &&
			!errors.Is(ferr, episode.ErrIllegalTransition) {
			log.Printf("reprise finish: fail episode %s: %v", episodeID, ferr)
		}
		return nil, err
	}
	if !moved {
		return out, nil
	}
	if err := j.advance(ctx, ownerID, episodeID, settledPass{}); err != nil {
		log.Printf("reprise finish: start passes for %s: %v", episodeID, err)
	}
	return out, nil
}

// markRendered makes the finished render the newest render row and moves
// a rendering episode to analysing, under the schedule lock. It reports
// false with no error when the episode already moved past rendering. A
// render that reused an older row appends that row again, so the newest
// row always names the file this render produced.
func (j *jobs) markRendered(ctx context.Context, ownerID, episodeID string, res render.Result) (bool, error) {
	j.schedMu.Lock()
	defer j.schedMu.Unlock()
	db := j.pipe.db
	state, err := episode.Current(ctx, db, episodeID)
	if err != nil {
		return false, err
	}
	if state != episode.StateRendering {
		return false, nil
	}
	if res.OpusMediaID == "" {
		return false, fmt.Errorf("reprise: render for %s stored no file", episodeID)
	}
	newest, err := newestRenderMedia(ctx, db.Reader(), episodeID)
	if err != nil {
		return false, err
	}
	if newest != res.OpusMediaID {
		if err := render.StoreRender(ctx, db.Writer(), ownerID, episodeID, res); err != nil {
			return false, err
		}
	}
	if err := episode.MarkRendered(ctx, db, episodeID); err != nil {
		return false, err
	}
	return true, nil
}

// newestRenderMedia reads the file the newest render row names, or empty
// when the episode holds no render.
func newestRenderMedia(ctx context.Context, db *sql.DB, episodeID string) (string, error) {
	var mediaID string
	err := db.QueryRowContext(ctx,
		`SELECT opus_media_id FROM renders WHERE episode_id = ? AND opus_media_id != ''
		 ORDER BY rowid DESC LIMIT 1`, episodeID).Scan(&mediaID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("reprise: read newest render: %w", err)
	}
	return mediaID, nil
}

// settledPass names the pass whose own job is settling. Its job record
// still reads running until the work returns, so the advance takes the
// pass outcome from here instead of from the record.
type settledPass struct {
	// kind is the settling pass kind, or empty when no pass settles.
	kind string
	// renderID is the render the settling pass worked on.
	renderID string
	// status is the terminal state the pass reached.
	status job.Status
}

// passKey names one pass after the render. The advance starts at most
// one pass per key, so the key names one job.
type passKey struct {
	episodeID string
	renderID  string
	kind      string
}

// passStatus returns the terminal status one pass reached from its error.
func passStatus(err error) job.Status {
	switch {
	case err == nil:
		return job.StatusDone
	case errors.Is(err, context.Canceled):
		return job.StatusCancelled
	default:
		return job.StatusError
	}
}

// advance moves one analysing episode on. It starts each pass after the
// render that never started on the newest render, and moves the episode
// to ready once analysis and marking both stopped. A pass whose work
// already returned counts as stopped even while its record still reads
// running, so two passes settling together never both wait on the other. A failed analysis
// starts no marking and still ships, because the render already plays.
// The cover never holds the episode. A pass kind at its limit starts
// later, on the next advance. Every start checks for a pass on the same
// render first, so a repeat starts nothing.
func (j *jobs) advance(ctx context.Context, ownerID, episodeID string, self settledPass) error {
	j.schedMu.Lock()
	defer j.schedMu.Unlock()
	if self.kind != "" {
		if j.settled == nil {
			j.settled = map[passKey]job.Status{}
		}
		j.settled[passKey{episodeID: episodeID, renderID: self.renderID, kind: self.kind}] = self.status
	}
	db := j.pipe.db
	state, err := episode.Current(ctx, db, episodeID)
	if errors.Is(err, episode.ErrNotFound) {
		j.forgetSettled(episodeID)
		return nil
	}
	if err != nil {
		return err
	}
	if state != episode.StateAnalysing {
		j.forgetSettled(episodeID)
		return nil
	}
	renderID, err := episode.NewestRenderID(ctx, db, episodeID)
	if err != nil {
		return err
	}
	if renderID == "" {
		return fmt.Errorf("reprise: analysing episode %s holds no render", episodeID)
	}
	read := func(kind string) (episode.Outcome, error) {
		pass, err := episode.LastRenderKindJob(ctx, db, episodeID, renderID, kind)
		if err != nil || !pass.Found {
			return pass, err
		}
		key := passKey{episodeID: episodeID, renderID: renderID, kind: kind}
		if jobTerminal(pass.Status) {
			delete(j.settled, key)
			return pass, nil
		}
		if status, ok := j.settled[key]; ok {
			pass.Status = string(status)
		}
		return pass, nil
	}
	ensure := func(kind string) (episode.Outcome, error) {
		pass, err := read(kind)
		if err != nil || pass.Found {
			return pass, err
		}
		jobID, err := j.startPass(ctx, kind, ownerID, episodeID, renderID)
		if errors.Is(err, job.ErrLimit) {
			return episode.Outcome{}, nil
		}
		if err != nil {
			return episode.Outcome{}, err
		}
		return episode.Outcome{Found: true, JobID: jobID, Status: string(job.StatusRunning)}, nil
	}
	analysed, err := ensure(kindAnalysis)
	if err != nil {
		return err
	}
	if _, err := ensure(kindCover); err != nil {
		return err
	}
	if !analysed.Found || !jobTerminal(analysed.Status) {
		return nil
	}
	if analysed.Status == string(job.StatusDone) {
		marked, err := ensure(kindMemory)
		if err != nil {
			return err
		}
		if !marked.Found || !jobTerminal(marked.Status) {
			return nil
		}
	}
	if err := episode.MarkReady(ctx, db, episodeID); err != nil && !errors.Is(err, episode.ErrIllegalTransition) {
		return err
	}
	j.forgetSettled(episodeID)
	return nil
}

// forgetSettled drops every settled outcome kept for one episode. It runs
// once the episode leaves analysing, because only an analysing episode
// reads them. So the map holds entries for analysing episodes alone. The
// caller holds schedMu.
func (j *jobs) forgetSettled(episodeID string) {
	for key := range j.settled {
		if key.episodeID == episodeID {
			delete(j.settled, key)
		}
	}
}

// startPass starts one pass after the render, stamped with the render it
// works on and chained to its settlement.
func (j *jobs) startPass(ctx context.Context, kind, ownerID, episodeID, renderID string) (string, error) {
	var inner job.Func
	switch kind {
	case kindAnalysis:
		inner = j.pipe.analysisFunc(ownerID, episodeID, renderID)
	case kindCover:
		inner = j.pipe.coverFunc(ownerID, episodeID)
	case kindMemory:
		inner = j.pipe.memoryFunc(ownerID, episodeID)
	default:
		return "", fmt.Errorf("reprise: no pass after the render runs as %q", kind)
	}
	desc := episodeDescriptor{OwnerID: ownerID, EpisodeID: episodeID, RenderID: renderID}
	chained := func(ctx context.Context, progress func(job.Progress)) ([]byte, error) {
		out, runErr := inner(ctx, withLinkage(progress, desc))
		j.settlePass(ctx, kind, desc, runErr)
		return out, runErr
	}
	return j.startStamped(ctx, kind, desc, chained)
}

// settlePass records one finished pass after the render. A cover pass
// that failed stores the plain cover, so the episode ships a cover either
// way. Then the episode moves on, with this pass read as finished.
func (j *jobs) settlePass(ctx context.Context, kind string, desc episodeDescriptor, runErr error) {
	settleCtx := context.WithoutCancel(ctx)
	if kind == kindCover && runErr != nil {
		if err := j.plainCover(settleCtx, desc.OwnerID, desc.EpisodeID); err != nil {
			log.Printf("reprise finish: plain cover for %s: %v", desc.EpisodeID, err)
		}
	}
	self := settledPass{kind: kind, renderID: desc.RenderID, status: passStatus(runErr)}
	if err := j.advance(settleCtx, desc.OwnerID, desc.EpisodeID, self); err != nil {
		log.Printf("reprise finish: advance %s after %s: %v", desc.EpisodeID, kind, err)
	}
}

// plainArt stands in for the cover model and its budget when the cover
// pass failed. Its model answers every call with an error, so the pass
// stores the deterministic panel, and it spends nothing. Its budget
// therefore books nothing. Keeping both halves on one value means the
// free budget can never sit beside a paid model.
type plainArt struct{}

// GenerateImage refuses every call, so the pass stores its fallback.
func (plainArt) GenerateImage(context.Context, string, cover.ImageRequest) (cover.ImageAnswer, error) {
	return cover.ImageAnswer{}, errPlainCover
}

// Reserve books nothing, because the plain model calls nothing paid.
func (plainArt) Reserve(cost.Price) error { return nil }

// Settle books nothing, because the plain model calls nothing paid.
func (plainArt) Settle(cost.Price, cost.Price) error { return nil }

// Release frees nothing, because nothing was held.
func (plainArt) Release(cost.Price) {}

// plainCover stores the deterministic cover for an episode that holds no
// cover row. An episode that already holds one keeps it.
func (j *jobs) plainCover(ctx context.Context, ownerID, episodeID string) error {
	held, err := hasCover(ctx, j.pipe.db.Reader(), episodeID)
	if err != nil {
		return err
	}
	if held {
		return nil
	}
	_, err = cover.Run(ctx, cover.Config{
		DB:        j.pipe.db.Writer(),
		Model:     plainArt{},
		Budgets:   plainArt{},
		ModelID:   j.pipe.editorialModel,
		OwnerID:   ownerID,
		EpisodeID: episodeID,
		Dir:       j.pipe.coverDir,
		SaveRaw: func(context.Context, []byte) error {
			return errPlainCover
		},
	})
	return err
}

// hasCover reports whether the episode holds a cover row. The cover pass
// creates its table on first use, so a missing table reads as no cover.
func hasCover(ctx context.Context, db *sql.DB, episodeID string) (bool, error) {
	var tables int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'covers'`).Scan(&tables); err != nil {
		return false, fmt.Errorf("reprise: find cover table: %w", err)
	}
	if tables == 0 {
		return false, nil
	}
	var covers int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM covers WHERE episode_id = ?`, episodeID).Scan(&covers); err != nil {
		return false, fmt.Errorf("reprise: count covers: %w", err)
	}
	return covers > 0, nil
}

// listInState returns every episode in one state with its owner, oldest
// first.
func listInState(ctx context.Context, db *sql.DB, state episode.State) ([]episodeDescriptor, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, owner_id FROM episodes WHERE state = ? ORDER BY rowid ASC`, string(state))
	if err != nil {
		return nil, fmt.Errorf("reprise: list %s episodes: %w", state, err)
	}
	defer rows.Close()
	var out []episodeDescriptor
	for rows.Next() {
		var d episodeDescriptor
		if err := rows.Scan(&d.EpisodeID, &d.OwnerID); err != nil {
			return nil, fmt.Errorf("reprise: list %s episodes: %w", state, err)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reprise: list %s episodes: %w", state, err)
	}
	return out, nil
}

// advanceAll moves every waiting episode on. It starts the renders a
// full render kind refused before. Then it starts the passes a full kind
// refused, and ships an episode whose passes stopped.
func (j *jobs) advanceAll(ctx context.Context) {
	j.startWaitingRenders(ctx)
	waiting, err := listInState(ctx, j.pipe.db.Reader(), episode.StateAnalysing)
	if err != nil {
		log.Printf("reprise finish: %v", err)
		return
	}
	for _, d := range waiting {
		if err := j.advance(ctx, d.OwnerID, d.EpisodeID, settledPass{}); err != nil {
			log.Printf("reprise finish: advance %s: %v", d.EpisodeID, err)
		}
	}
}

// startWaitingRenders starts a render for each rendering episode that no
// render job ever served, oldest first. The render kind runs one job at a
// time, so a refused start stops the walk. The episode keeps its state,
// and the next advance tries it again. One render therefore runs at a
// time, and every waiting episode gets its turn.
func (j *jobs) startWaitingRenders(ctx context.Context) {
	waiting, err := listInState(ctx, j.pipe.db.Reader(), episode.StateRendering)
	if err != nil {
		log.Printf("reprise finish: %v", err)
		return
	}
	for _, d := range waiting {
		err := j.startWaitingRender(ctx, d.OwnerID, d.EpisodeID)
		if errors.Is(err, job.ErrLimit) {
			return
		}
		if err != nil {
			log.Printf("reprise finish: render for %s: %v", d.EpisodeID, err)
		}
	}
}

// startWaitingRender starts the render for one rendering episode that no
// render job ever served. It reads the state and the render job under the
// schedule lock, so a start that raced it starts nothing twice. An
// episode with any render job is left to that job.
func (j *jobs) startWaitingRender(ctx context.Context, ownerID, episodeID string) error {
	j.schedMu.Lock()
	defer j.schedMu.Unlock()
	state, err := episode.Current(ctx, j.pipe.db, episodeID)
	if err != nil {
		return err
	}
	if state != episode.StateRendering {
		return nil
	}
	last, err := episode.LastKindJob(ctx, j.pipe.db, episodeID, kindRender)
	if err != nil {
		return err
	}
	if last.Found {
		return nil
	}
	desc := episodeDescriptor{OwnerID: ownerID, EpisodeID: episodeID}
	_, err = j.startStamped(ctx, kindRender, desc, j.renderFunc(ownerID, episodeID))
	return err
}

// advanceEvery returns the advance loop period, advanceInterval unless
// the boot set another.
func (j *jobs) advanceEvery() time.Duration {
	if j.advancePeriod > 0 {
		return j.advancePeriod
	}
	return advanceInterval
}

// runAdvanceLoop advances every waiting episode once per interval until
// ctx ends.
func (j *jobs) runAdvanceLoop(ctx context.Context) {
	tick := time.NewTicker(j.advanceEvery())
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			j.advanceAll(ctx)
		}
	}
}

// recoverFinish settles what a restart left between mark done and ready.
// A rendering episode whose render resumes waits for it. One whose render
// finished moves on. One whose render stopped for good fails, so the card
// names the render. The advance then starts a render for each episode
// whose render never started, because a render calls nothing paid. A
// render already running holds the one render slot, so the rest wait for
// later advances. Every analysing episode then moves on, which ships the
// episodes whose paid passes a restart interrupted.
func (j *jobs) recoverFinish(ctx context.Context) {
	rendering, err := listInState(ctx, j.pipe.db.Reader(), episode.StateRendering)
	if err != nil {
		log.Printf("reprise recover: %v", err)
	}
	for _, d := range rendering {
		if err := j.recoverRender(ctx, d.OwnerID, d.EpisodeID); err != nil {
			log.Printf("reprise recover: render for %s: %v", d.EpisodeID, err)
		}
	}
	j.advanceAll(ctx)
}

// recoverRender settles one rendering episode after a restart. An
// episode whose render never started waits for the advance to start it.
func (j *jobs) recoverRender(ctx context.Context, ownerID, episodeID string) error {
	last, err := episode.LastKindJob(ctx, j.pipe.db, episodeID, kindRender)
	if err != nil {
		return err
	}
	if !last.Found || !jobTerminal(last.Status) {
		return nil
	}
	if last.Status != string(job.StatusDone) {
		return episode.Transition(ctx, j.pipe.db, episodeID, episode.StateRendering, episode.StateFailed)
	}
	rec, err := j.store.Get(ctx, last.JobID)
	if err != nil {
		return err
	}
	_, err = j.settleRender(ctx, ownerID, episodeID, rec.Data, nil)
	return err
}
