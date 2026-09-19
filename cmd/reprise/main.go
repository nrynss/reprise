// Command reprise serves the built web application and a health endpoint.
// It is one static binary for one distroless container, so it reads the app
// from disk instead of embedding it. The Docker build stages web/build next
// to the binary and the container needs no rebuild to ship a web change.
// Settings load once at boot from the file named by REPRISE_CONFIG. The boot
// log prints the resolution plan, which names every source and carries no
// value. SIGTERM starts a drain: new sessions stop, open ones run to their
// cap, and only then does the process exit.
package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"math"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/nrynss/keel/cost"
	costsqlitestore "github.com/nrynss/keel/cost/sqlitestore"
	"github.com/nrynss/keel/ffmpeg"
	flagsqlitestore "github.com/nrynss/keel/flag/sqlitestore"
	"github.com/nrynss/keel/gate"
	"github.com/nrynss/keel/id"
	"github.com/nrynss/keel/job"
	jobsqlitestore "github.com/nrynss/keel/job/sqlitestore"
	"github.com/nrynss/keel/lease"
	leasesqlitestore "github.com/nrynss/keel/lease/sqlitestore"
	"github.com/nrynss/keel/mediastore"
	mediasqlitestore "github.com/nrynss/keel/mediastore/sqlitestore"
	"github.com/nrynss/keel/sqlite"
	"github.com/nrynss/keel/stream"
	"github.com/nrynss/keel/upload"
	"github.com/nrynss/keel/wire"
	"github.com/nrynss/reprise/internal/analysis"
	"github.com/nrynss/reprise/internal/api"
	"github.com/nrynss/reprise/internal/assemblyai"
	"github.com/nrynss/reprise/internal/broker"
	"github.com/nrynss/reprise/internal/cover"
	"github.com/nrynss/reprise/internal/editorial"
	"github.com/nrynss/reprise/internal/episode"
	"github.com/nrynss/reprise/internal/gemini"
	"github.com/nrynss/reprise/internal/host"
	"github.com/nrynss/reprise/internal/identity"
	"github.com/nrynss/reprise/internal/limits"
	"github.com/nrynss/reprise/internal/memory"
	"github.com/nrynss/reprise/internal/render"
	"github.com/nrynss/reprise/internal/settings"
	reprisestore "github.com/nrynss/reprise/internal/store"
	"github.com/nrynss/reprise/internal/transcript"
)

// version names the build the health endpoint reports. The image build sets
// it to the commit through ldflags. A local binary reports dev.
var version = "dev"

// bootID names this process instance in the health response, so an operator
// can tell a fresh container from one that survived a restart.
var bootID string

// fallbackDrainSeconds bounds the drain when the settings file names no
// positive session cap. It matches the box session cap, so a session that
// started just before the signal still gets its full length.
const fallbackDrainSeconds = 1800

// httpCloseGrace bounds the final HTTP close after the session drain ends.
// In flight API calls are short, so a short grace is enough. Sessions set
// the drain budget, never this grace.
const httpCloseGrace = 30 * time.Second

// concurrentSessionSlots bounds how many session leases stay open at once
// across every guest. A mint past the bound refuses with the slots code,
// so one rush of starts cannot overlap more live sessions than this.
const concurrentSessionSlots = 8

// nanosPerCent converts the daily spend ceiling from the cents the settings
// file carries to the nanodollars the budget stores enforce.
const nanosPerCent = 10_000_000

// mediaContentTypes is the closed set the blob store persists. It carries
// the image, audio, video and subtitle types the media library accepts by
// default, plus the MP4 audio the render writes, which the default set
// leaves out, and the JSON provider receipts the passes persist, because
// provider output persists on receipt and artifact URLs expire.
var mediaContentTypes = []string{
	"image/png",
	"image/jpeg",
	"image/webp",
	"audio/mpeg",
	"audio/wav",
	"audio/ogg",
	"audio/webm",
	"audio/mp4",
	"video/mp4",
	"application/pdf",
	"text/vtt",
	"application/x-subrip",
	"application/json",
}

// drainPollInterval spaces the open session checks during a drain. One
// second is frequent enough to notice an ended session and rare enough to
// keep the drain log quiet.
const drainPollInterval = time.Second

// sessions reports how many live sessions stay open. The lease manager
// behind the session broker implements it, so the drain waits on the same
// rows the broker opens. A nil value means sessions are not wired yet, and
// the drain waits on HTTP alone.
type sessions interface {
	Active() int
}

// drainingGate refuses new sessions while the process drains. Open sessions
// keep their connections, so only the session start path closes. The gate
// answers through the shared envelope, so screens parse it like any refusal.
type drainingGate struct {
	handler  http.Handler
	draining atomic.Bool
}

// ServeHTTP answers POST /api/sessions with 503 once draining. Every other
// request passes through untouched.
func (g *drainingGate) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if g.draining.Load() && r.Method == http.MethodPost && r.URL.Path == "/api/sessions" {
		_ = wire.WriteError(w, http.StatusServiceUnavailable, "server_draining", "this host is draining and takes no new sessions", nil)
		return
	}
	g.handler.ServeHTTP(w, r)
}

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	webDir := flag.String("web", "web/build", "directory holding the built web application")
	flag.Parse()

	loaded, plan, err := settings.Load(context.Background())
	if err != nil {
		log.Fatalf("load settings: %v", err)
	}
	log.Printf("reprise settings:\n%s", plan.String())

	boot, err := id.New()
	if err != nil {
		log.Fatalf("generate boot id: %v", err)
	}
	bootID = boot

	gate := &drainingGate{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handleHealth)
	sweepCtx, stopSweep := context.WithCancel(context.Background())
	defer stopSweep()
	leases, err := wireAPI(sweepCtx, mux, loaded)
	if err != nil {
		log.Fatalf("wire routes: %v", err)
	}
	mux.Handle("/", appHandler(*webDir))
	gate.handler = mux

	srv := &http.Server{Addr: *addr, Handler: gate}

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sigs)

	log.Printf("reprise listening on %s, serving %s", *addr, *webDir)
	if err := run(srv, gate, leases, drainBudget(loaded), sigs); err != nil {
		log.Fatalf("serve: %v", err)
	}
}

// drainBudget bounds the SIGTERM drain by the session cap, so a session that
// started just before the signal still gets its full length.
func drainBudget(loaded settings.Settings) time.Duration {
	if loaded.SessionMaxSeconds <= 0 {
		return fallbackDrainSeconds * time.Second
	}
	return time.Duration(loaded.SessionMaxSeconds) * time.Second
}

// run serves until a signal arrives or the server fails, then drains. It
// stops new sessions first, waits for open ones through reg up to budget,
// and only then closes HTTP. A nil reg means sessions are not wired yet.
func run(srv *http.Server, gate *drainingGate, reg sessions, budget time.Duration, sigs <-chan os.Signal) error {
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- srv.ListenAndServe()
	}()

	select {
	case err := <-serveErr:
		return fmt.Errorf("reprise: listen: %w", err)
	case sig := <-sigs:
		log.Printf("reprise received %s, draining open sessions", sig)
	}

	gate.draining.Store(true)

	drainCtx, drainCancel := context.WithTimeout(context.Background(), budget)
	defer drainCancel()
	waitSessions(drainCtx, reg, drainPollInterval)

	closeCtx, closeCancel := context.WithTimeout(context.Background(), httpCloseGrace)
	defer closeCancel()
	if err := srv.Shutdown(closeCtx); err != nil {
		return fmt.Errorf("reprise: shutdown: %w", err)
	}
	if err := <-serveErr; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("reprise: listen: %w", err)
	}
	return nil
}

// waitSessions blocks until no session stays open or ctx ends. It polls the
// registry instead of holding a lock, because sessions open and close on
// other goroutines. A nil registry waits on nothing.
func waitSessions(ctx context.Context, reg sessions, poll time.Duration) {
	if reg == nil {
		return
	}
	tick := time.NewTicker(poll)
	defer tick.Stop()
	for reg.Active() > 0 {
		select {
		case <-ctx.Done():
			log.Printf("reprise drain ended with %d sessions open", reg.Active())
			return
		case <-tick.C:
		}
	}
}

// handleHealth answers GET /healthz with 200. The body names the process
// and the build, so a probe against the wrong container or the wrong commit
// is visible in the response itself.
func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintf(w, "ok %s version=%s\n", bootID, version)
}

// appHandler serves the built application with a single-page fallback.
// The build emits the client shell as fallback.html, and unknown paths
// return it so client-side routes survive a reload.
func appHandler(webDir string) http.Handler {
	root := http.FileServer(http.Dir(webDir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path != "/" && path != "/index.html" {
			if _, err := os.Stat(filepath.Join(webDir, path)); errors.Is(err, fs.ErrNotExist) {
				http.ServeFile(w, r, filepath.Join(webDir, "fallback.html"))
				return
			}
		}
		root.ServeHTTP(w, r)
	})
}

// hostBuilder adapts the host prompt loader to the broker config seam. The
// broker declares the interface and the host owns the rows, so this type is
// the one place the two meet.
type hostBuilder struct {
	db *sql.DB
}

// BuildSessionConfig loads the session config for one owner from stored
// rows. Counts and names in it come from those rows, never from invention.
func (b hostBuilder) BuildSessionConfig(ctx context.Context, ownerID string) (broker.SessionConfig, error) {
	cfg, err := host.Load(ctx, b.db, ownerID)
	if err != nil {
		return broker.SessionConfig{}, err
	}
	return broker.SessionConfig{
		SystemPrompt: cfg.SystemPrompt,
		Greeting:     cfg.Greeting,
		Keyterms:     cfg.Keyterms,
	}, nil
}

// spendCeiling converts the daily spend ceiling from the cents the settings
// file carries to the nanodollars the budget stores enforce. It refuses a
// negative ceiling and a value that leaves the int64 range, so a broken
// file stops the process instead of wrapping into a false ceiling.
func spendCeiling(cents int64) (cost.Price, error) {
	if cents < 0 {
		return 0, fmt.Errorf("reprise: daily spend %d cents: ceiling must not be negative", cents)
	}
	if cents > math.MaxInt64/nanosPerCent {
		return 0, fmt.Errorf("reprise: daily spend %d cents: ceiling leaves the int64 range", cents)
	}
	return cost.Price(cents) * nanosPerCent, nil
}

// wireAPI opens the stores every mounted route needs and registers the
// route table on mux. It changes nothing about boot, drain, health, or the
// served app shell. A failure stops the process at the call site, the way
// a missing secret does. It returns the lease registry for the drain,
// and starts the abandoned sweep on its schedule under ctx.
//
// One owner may spend up to the whole daily ceiling. The per-owner ceiling
// still isolates accounting and lets the admin page lower one owner, while
// the global ceiling caps the day across owners. Both constructors take
// this one value, and the mount refuses to wire them if they ever disagree.
func wireAPI(ctx context.Context, mux *http.ServeMux, loaded settings.Settings) (*lease.Manager, error) {
	signingKey, err := loaded.Secrets.SessionSigningKey.Reveal()
	if err != nil {
		return nil, fmt.Errorf("reprise: reveal session signing key: %w", err)
	}
	apiKey, err := loaded.Secrets.AssemblyAIAPIKey.Reveal()
	if err != nil {
		return nil, fmt.Errorf("reprise: reveal provider key: %w", err)
	}
	ceiling, err := spendCeiling(loaded.DailySpendCents)
	if err != nil {
		return nil, err
	}
	db, err := sqlite.Open(ctx, sqlite.Config{Path: filepath.Join(loaded.DataDir, "reprise.db")})
	if err != nil {
		return nil, fmt.Errorf("reprise: open database: %w", err)
	}
	wired := false
	defer func() {
		if !wired {
			_ = db.Close()
		}
	}()
	if _, err := reprisestore.Open(ctx, db); err != nil {
		return nil, fmt.Errorf("reprise: migrate diary schema: %w", err)
	}
	if err := memory.Migrate(ctx, db); err != nil {
		return nil, fmt.Errorf("reprise: migrate memory schema: %w", err)
	}
	identitySvc, err := identity.New(ctx, identity.Config{DB: db, SigningKey: signingKey})
	if err != nil {
		return nil, fmt.Errorf("reprise: open guest sessions: %w", err)
	}
	costStore, err := costsqlitestore.Open(ctx, costsqlitestore.Config{DB: db, Limit: ceiling, ReservationTTL: settleTTL(loaded)})
	if err != nil {
		return nil, fmt.Errorf("reprise: open spend ledger: %w", err)
	}
	keyed := costsqlitestore.NewKeyedBudget(costStore)
	flagStore, err := flagsqlitestore.Open(ctx, flagsqlitestore.Config{DB: db})
	if err != nil {
		return nil, fmt.Errorf("reprise: open runtime switches: %w", err)
	}
	quota, err := lease.NewQuota(concurrentSessionSlots)
	if err != nil {
		return nil, fmt.Errorf("reprise: size session quota: %w", err)
	}
	leaseStore, err := leasesqlitestore.Open(ctx, leasesqlitestore.Config{DB: db})
	if err != nil {
		return nil, fmt.Errorf("reprise: open session leases: %w", err)
	}
	minter, err := assemblyai.NewClient(assemblyai.TokenBaseURL, apiKey, nil)
	if err != nil {
		return nil, fmt.Errorf("reprise: open token client: %w", err)
	}
	diary, err := broker.NewSQLiteDiary(db)
	if err != nil {
		return nil, fmt.Errorf("reprise: open session diary: %w", err)
	}
	sessionBroker, err := broker.New(broker.Config{
		Flags:             flagStore,
		Budgets:           keyed,
		DB:                db,
		LeaseQuota:        quota,
		LeaseStore:        leaseStore,
		Minter:            minter,
		Sessions:          hostBuilder{db: db.Writer()},
		Diary:             diary,
		SessionCapSeconds: loaded.SessionMaxSeconds,
		GuestMaxSessions:  loaded.GuestMaxSessions,
		OwnerSessionLimit: ceiling,
	})
	if err != nil {
		return nil, fmt.Errorf("reprise: open session broker: %w", err)
	}
	adminSvc, err := limits.New(limits.Config{
		Flags:             flagStore,
		Budgets:           keyed,
		Global:            costStore,
		Auth:              limits.StubOwnerAuth{},
		GuestMaxSessions:  loaded.GuestMaxSessions,
		SessionMaxSeconds: loaded.SessionMaxSeconds,
		DailySpendCents:   loaded.DailySpendCents,
		OwnerDefaultLimit: ceiling,
	})
	if err != nil {
		return nil, fmt.Errorf("reprise: open guest limits: %w", err)
	}
	mediaIndex, err := mediasqlitestore.Open(ctx, mediasqlitestore.Config{DB: db})
	if err != nil {
		return nil, fmt.Errorf("reprise: open media index: %w", err)
	}
	mediaStore, err := mediastore.Open(ctx, mediastore.Config{
		Dir:          loaded.MediaDir,
		Index:        mediaIndex,
		ContentTypes: mediaContentTypes,
		Authorize:    identitySvc.AuthorizeMedia,
	})
	if err != nil {
		return nil, fmt.Errorf("reprise: open media store: %w", err)
	}
	uploadHandler, err := upload.New(upload.Config{
		Dir:      filepath.Join(loaded.MediaDir, "upload-stage"),
		Store:    mediaStore,
		BasePath: api.UploadBasePath,
	})
	if err != nil {
		return nil, fmt.Errorf("reprise: open upload handler: %w", err)
	}
	spendGate, err := gate.New(gate.Config{})
	if err != nil {
		return nil, fmt.Errorf("reprise: open spend gate: %w", err)
	}
	events := stream.New(stream.Config{})
	jobs, err := openJobs(ctx, db, events, loaded, keyed, mediaStore, render.StoreMedia(mediaStore, mediaIndex), sessionBroker, diary)
	if err != nil {
		return nil, err
	}
	episodeSvc, err := episode.NewService(episode.Config{
		DB:         db,
		Starter:    jobs.runner,
		RenderKind: kindRender,
		Render:     jobs.resolver,
	})
	if err != nil {
		return nil, fmt.Errorf("reprise: open episode service: %w", err)
	}
	rule := gate.Rule{
		Name:      "api",
		PerClient: gate.Limit{Burst: 16, Every: time.Minute},
		Global:    gate.Limit{Burst: 256, Every: time.Minute},
	}
	if err := api.Mount(mux, api.Dependencies{
		Gate:              spendGate,
		Rule:              rule,
		Identity:          identitySvc,
		Sessions:          sessionBroker,
		Episodes:          api.NewEpisodes(episodeSvc),
		SessionEnd:        jobs.settleEnd(api.NewSessionEnd(episodeSvc)),
		Threads:           api.NewThreads(db),
		Admin:             adminSvc.Handler(),
		Uploads:           uploadHandler,
		Media:             mediaStore,
		Events:            events,
		OwnerSessionLimit: ceiling,
		OwnerDefaultLimit: ceiling,
	}); err != nil {
		return nil, fmt.Errorf("reprise: mount routes: %w", err)
	}
	uploadHandler.Start()
	go jobs.runSweepLoop(ctx)
	wired = true
	return sessionBroker.Leases(), nil
}

// Pipeline kind names. The phase packages set limits and resumption
// per kind, and the binary names each kind once, so starters, the
// runner, and the restart pass agree on one literal per pass.
const (
	// kindEditTranscript runs the user stem batch transcription.
	kindEditTranscript = "edit_transcript"
	// kindEditorial runs the Gemini editorial pass.
	kindEditorial = "editorial"
	// kindRender runs the ffmpeg render.
	kindRender = "render"
	// kindAnalysis runs the render batch pass with chapters.
	kindAnalysis = "analysis"
	// kindCover draws the episode cover.
	kindCover = "cover"
	// kindMemory marks commitments in the rendered transcript.
	kindMemory = "memory"
)

// sweepInterval spaces abandoned sweep runs. Five minutes bounds the
// meter on a dropped session while keeping empty passes cheap: an
// empty listing settles nothing and ends one job.
const sweepInterval = 5 * time.Minute

// sweepLag covers the settle delay past the sweep tick, so a hold
// outlives the sweep that frees it.
const sweepLag = 5 * time.Minute

// Gemini text token rates in dollars per million tokens. The settings
// catalog should carry these beside the model id, and no catalog
// exists yet, so the wiring pins the published Vertex rates here
// until that seam lands. Every paid call still reserves first, so a
// wrong rate mistates spend instead of spending freely.
const (
	geminiPromptPerMillionUSD     = 0.30
	geminiCompletionPerMillionUSD = 2.50
)

// memoryMaxOutputTokens caps the marking answer the estimate books. It
// matches the answer cap the marking calls send, so the reservation
// covers the dearest answer the model may return.
const memoryMaxOutputTokens = 2000

// paidKinds names the job kinds that call paid APIs. A restart marks
// them interrupted and fails their episodes, so no paid call reruns
// silently. Idempotent kinds resume instead and keep their episodes.
var paidKinds = map[string]bool{
	kindEditTranscript: true,
	kindEditorial:      true,
	kindAnalysis:       true,
	kindCover:          true,
	kindMemory:         true,
}

// settleTTL bounds one session spend hold. It covers the session cap
// plus one sweep interval and settle lag, so a hold outlives the
// session it prices and the sweep that frees it. The library default
// expires sooner than the session cap, which would return headroom
// while the meter still runs.
func settleTTL(loaded settings.Settings) time.Duration {
	cap := loaded.SessionMaxSeconds
	if cap <= 0 {
		cap = fallbackDrainSeconds
	}
	return time.Duration(cap)*time.Second + sweepInterval + sweepLag
}

// ownerBudget adapts the durable keyed budget to the single job Budget
// seam the passes declare. One adapter serves one job: Reserve holds
// the estimate on both ceilings under the owner, and Settle or Release
// frees that same hold. A first visit provisions the owner ceiling at
// the daily default, the way the session broker does. The pass seam
// carries no context, so the fast local store calls run unscoped.
type ownerBudget struct {
	budgets *costsqlitestore.KeyedBudget
	owner   string
	limit   cost.Price
	mu      sync.Mutex
	held    costsqlitestore.Reservation
}

// Reserve holds estimate against the owner and global ceilings.
func (b *ownerBudget) Reserve(estimate cost.Price) error {
	ctx := context.Background()
	held, err := b.budgets.Reserve(ctx, b.owner, estimate)
	if errors.Is(err, cost.ErrUnknownOwner) {
		if setErr := b.budgets.SetLimit(ctx, b.owner, b.limit); setErr != nil {
			return setErr
		}
		held, err = b.budgets.Reserve(ctx, b.owner, estimate)
	}
	if err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.held = held
	return nil
}

// Settle books the measured price and frees the reservation.
func (b *ownerBudget) Settle(reserved, actual cost.Price) error {
	b.mu.Lock()
	held := b.held
	b.mu.Unlock()
	_ = reserved
	return b.budgets.Settle(context.Background(), b.owner, held, actual)
}

// Release frees a reservation the caller never spent.
func (b *ownerBudget) Release(reserved cost.Price) {
	b.mu.Lock()
	held := b.held
	b.mu.Unlock()
	_ = reserved
	_ = b.budgets.Release(context.Background(), b.owner, held)
}

// geminiChapters asks the shared client for episode chapters. The
// analysis package owns the chapter shaping, so this type only ferries
// the call and decodes the drafts the schema promised.
type geminiChapters struct {
	client *gemini.Client
}

// chapterDraftJSON decodes one chapter draft the schema promised.
type chapterDraftJSON struct {
	// Title is the chapter title as returned.
	Title string `json:"title"`
	// StartMs is the chapter start in milliseconds from the render start.
	StartMs int64 `json:"start_ms"`
}

// chaptersJSON decodes the chapter answer the schema promised.
type chaptersJSON struct {
	// Chapters holds the raw chapters in episode order.
	Chapters []chapterDraftJSON `json:"chapters"`
}

// CompleteChapters asks the named model for episode chapters.
func (c geminiChapters) CompleteChapters(ctx context.Context, model string, req analysis.ChapterRequest) (analysis.ChapterAnswer, error) {
	text, usage, err := c.client.CompleteChapters(ctx, model, req.Transcript, req.DurationSecs, req.MaxTokens)
	if err != nil {
		return analysis.ChapterAnswer{}, err
	}
	var decoded chaptersJSON
	if err := json.Unmarshal([]byte(text), &decoded); err != nil {
		return analysis.ChapterAnswer{}, fmt.Errorf("reprise: decode chapters: %w", err)
	}
	drafts := make([]analysis.ChapterDraft, 0, len(decoded.Chapters))
	for _, draft := range decoded.Chapters {
		drafts = append(drafts, analysis.ChapterDraft{Title: draft.Title, StartMs: draft.StartMs})
	}
	return analysis.ChapterAnswer{
		Drafts: drafts,
		Usage:  analysis.Usage{PromptTokens: usage.Prompt, CompletionTokens: usage.Candidates},
		Raw:    []byte(text),
	}, nil
}

// geminiCover draws episode covers through the shared client. The cover
// pass owns the brief and the fallback, so this type only ferries the
// call. A model failure returns the error, and the pass stores its
// deterministic fallback instead.
type geminiCover struct {
	client *gemini.Client
}

// GenerateImage draws one square panel from the brief.
func (m geminiCover) GenerateImage(ctx context.Context, model string, req cover.ImageRequest) (cover.ImageAnswer, error) {
	png, err := m.client.GenerateImage(ctx, model, req.Prompt)
	if err != nil {
		return cover.ImageAnswer{}, err
	}
	return cover.ImageAnswer{Image: png}, nil
}

// geminiMemory marks transcripts through the shared client. Prompts and
// parsing stay in the memory package, so this type only ferries text
// both ways.
type geminiMemory struct {
	client *gemini.Client
}

// GenerateCommitments lists things the speaker said they would do.
func (m geminiMemory) GenerateCommitments(ctx context.Context, model string, req memory.CommitmentRequest) (memory.CommitmentAnswer, error) {
	text, _, err := m.client.GenerateCommitments(ctx, model, req.Transcript)
	if err != nil {
		return memory.CommitmentAnswer{}, err
	}
	return memory.CommitmentAnswer{JSON: text}, nil
}

// GenerateResolution judges whether the later transcript reports doing
// the commitment.
func (m geminiMemory) GenerateResolution(ctx context.Context, model string, req memory.ResolutionRequest) (memory.ResolutionAnswer, error) {
	text, _, err := m.client.GenerateResolution(ctx, model, req.Commitment, req.Transcript)
	if err != nil {
		return memory.ResolutionAnswer{}, err
	}
	return memory.ResolutionAnswer{JSON: text}, nil
}

// batchTranscriber serves the analysis batch seam over the AssemblyAI
// batch client. Words, text, duration, and delete confirmation map one
// to one. Entity, phrase, and summary features need a provider adapter
// that asks for them, which no landed package builds yet, so those
// stay empty until that seam lands and the analysis pass fills them.
type batchTranscriber struct {
	batch *assemblyai.BatchClient
}

// Upload posts render bytes and returns the URL the create call reads.
func (t batchTranscriber) Upload(ctx context.Context, audio io.Reader) (string, error) {
	return t.batch.Upload(ctx, audio)
}

// Create starts one transcription and returns its id.
func (t batchTranscriber) Create(ctx context.Context, req analysis.CreateRequest) (string, error) {
	return t.batch.Create(ctx, assemblyai.CreateRequest{AudioURL: req.AudioURL})
}

// Wait polls one transcript until it completes or fails.
func (t batchTranscriber) Wait(ctx context.Context, id string, interval time.Duration) (analysis.TranscriptResult, error) {
	done, err := t.batch.Wait(ctx, id, interval)
	if err != nil {
		return analysis.TranscriptResult{}, err
	}
	return mapTranscript(done), nil
}

// Get fetches one transcript with its receipt bytes.
func (t batchTranscriber) Get(ctx context.Context, id string) (analysis.TranscriptResult, error) {
	done, err := t.batch.Get(ctx, id)
	if err != nil {
		return analysis.TranscriptResult{}, err
	}
	return mapTranscript(done), nil
}

// Delete removes one transcript by id.
func (t batchTranscriber) Delete(ctx context.Context, id string) error {
	return t.batch.Delete(ctx, id)
}

// mapTranscript carries the batch fields the provider returns today:
// words, text, billed length, and the receipt. Entity and phrase rows
// stay empty until a provider adapter asks for those features.
func mapTranscript(done assemblyai.Transcript) analysis.TranscriptResult {
	words := make([]analysis.Word, 0, len(done.Words))
	for _, word := range done.Words {
		words = append(words, analysis.Word{
			Text:       word.Text,
			StartMs:    word.StartMs,
			EndMs:      word.EndMs,
			Confidence: word.Confidence,
		})
	}
	return analysis.TranscriptResult{
		ID:                done.ID,
		Text:              done.Text,
		Words:             words,
		AudioDurationSecs: done.AudioDurationSecs,
		Raw:               done.Raw,
	}
}

// episodeDescriptor travels in pipeline job progress, so the restart
// pass maps an interrupted paid job back to its episode. Render and
// reconcile records already carry these keys, so one shape reads all
// three.
type episodeDescriptor struct {
	// OwnerID scopes the episode the job worked.
	OwnerID string `json:"owner_id"`
	// EpisodeID scopes the episode the job worked.
	EpisodeID string `json:"episode_id"`
}

// reportEpisode publishes the episode linkage first, so an
// interruption still leaves the record carrying enough to fail the
// episode instead of losing it.
func reportEpisode(progress func(job.Progress), ownerID, episodeID string) {
	if progress == nil {
		return
	}
	raw, err := json.Marshal(episodeDescriptor{OwnerID: ownerID, EpisodeID: episodeID})
	if err != nil {
		return
	}
	progress(job.Progress{Stage: "start", Detail: raw})
}

// pipeline binds every long pass to its shared clients and settings
// models. One value serves the whole process, so the Gemini client is
// built once and each paid kind carries the settings model id. Model
// adapters are built once beside it, so a test can prove the sharing
// instead of trusting it.
type pipeline struct {
	db             *sqlite.DB
	gemini         *gemini.Client
	batch          *assemblyai.BatchClient
	budgets        *costsqlitestore.KeyedBudget
	ownerLimit     cost.Price
	editorialModel string
	chapterRates   analysis.Rates
	markingRates   memory.Rates
	chapters       geminiChapters
	coverModel     geminiCover
	memoryModel    geminiMemory
	transcriber    batchTranscriber
	media          *mediastore.Store
	mediaDir       string
	coverDir       string
	renderWorkDir  string
	renderTools    ffmpeg.Tools
}

// newPipeline binds the pipeline over one shared Gemini client. The
// caller builds the client once from the file credential, and every
// model backed pass shares it. The editorial settings model drives
// editorial, chapters, cover, and marking alike, because the settings
// file names no other Gemini model and the model id never comes from
// code.
func newPipeline(db *sqlite.DB, geminiClient *gemini.Client, batch *assemblyai.BatchClient, budgets *costsqlitestore.KeyedBudget, ownerLimit cost.Price, editorialModel string, media *mediastore.Store, mediaDir, coverDir, renderWorkDir string) *pipeline {
	return &pipeline{
		db:             db,
		gemini:         geminiClient,
		batch:          batch,
		budgets:        budgets,
		ownerLimit:     ownerLimit,
		editorialModel: editorialModel,
		chapterRates: analysis.Rates{
			PromptPerMillionUSD:     geminiPromptPerMillionUSD,
			CompletionPerMillionUSD: geminiCompletionPerMillionUSD,
		},
		markingRates: memory.Rates{
			PromptPerMillionUSD:     geminiPromptPerMillionUSD,
			CompletionPerMillionUSD: geminiCompletionPerMillionUSD,
			MaxOutputTokens:         memoryMaxOutputTokens,
		},
		chapters:      geminiChapters{client: geminiClient},
		coverModel:    geminiCover{client: geminiClient},
		memoryModel:   geminiMemory{client: geminiClient},
		transcriber:   batchTranscriber{batch: batch},
		media:         media,
		mediaDir:      mediaDir,
		coverDir:      coverDir,
		renderWorkDir: renderWorkDir,
	}
}

// budgetsFor bounds one job on the durable ceilings under its owner.
func (p *pipeline) budgetsFor(owner string) *ownerBudget {
	return &ownerBudget{budgets: p.budgets, owner: owner, limit: p.ownerLimit}
}

// receiptSink persists one provider response privately under the
// episode, because provider output persists on receipt and artifact
// URLs expire.
func (p *pipeline) receiptSink(ownerID, episodeID string) func(ctx context.Context, raw []byte) error {
	return func(ctx context.Context, raw []byte) error {
		if len(raw) == 0 {
			return fmt.Errorf("reprise: persist receipt: empty response")
		}
		_, err := p.media.Persist(ctx, bytes.NewReader(raw), mediastore.Put{
			ContentType: "application/json",
			Owner:       ownerID,
			Group:       episodeID,
			Visibility:  mediastore.Private,
		})
		return err
	}
}

// kinds registers every pipeline and settle kind on the runner. Paid
// kinds stay non-idempotent with no resume, so a restart marks them
// interrupted and fails their episodes instead of paying twice. The
// render, reconcile, and sweep kinds resume through their resolvers.
func (p *pipeline) kinds(resolver *render.Resolver, rec *broker.Reconciler, sweeper *broker.Sweeper) map[string]job.Kind {
	return map[string]job.Kind{
		kindEditTranscript:   transcript.Kind,
		kindEditorial:        editorial.Kind,
		kindRender:           render.KindOf(resolver),
		kindAnalysis:         analysis.Kind,
		kindCover:            cover.Kind,
		kindMemory:           job.Kind{Limit: 1},
		broker.KindName:      rec.Kind(),
		broker.SweepKindName: sweeper.Kind(),
	}
}

// editTranscriptFunc builds the edit transcript work for one episode.
// Stems, timings, and offsets arrive per schedule, because only the
// upload completion knows them.
func (p *pipeline) editTranscriptFunc(ownerID, episodeID string, audio []byte, durationSecs float64, host []transcript.HostReply, offsets transcript.Offsets) job.Func {
	return func(ctx context.Context, progress func(job.Progress)) ([]byte, error) {
		reportEpisode(progress, ownerID, episodeID)
		res, err := transcript.Run(ctx, transcript.Config{
			DB:           p.db.Writer(),
			Batch:        p.batch,
			Budgets:      p.budgetsFor(ownerID),
			OwnerID:      ownerID,
			EpisodeID:    episodeID,
			Audio:        audio,
			DurationSecs: durationSecs,
			Host:         host,
			Offsets:      offsets,
			SaveRaw:      p.receiptSink(ownerID, episodeID),
		})
		if err != nil {
			return nil, err
		}
		return json.Marshal(res)
	}
}

// editorialFunc builds the editorial work for one episode. Stems arrive
// per schedule, because only the upload completion holds them.
func (p *pipeline) editorialFunc(ownerID, episodeID string, userStem, hostStem []byte, durationSecs float64) job.Func {
	return func(ctx context.Context, progress func(job.Progress)) ([]byte, error) {
		reportEpisode(progress, ownerID, episodeID)
		res, err := editorial.Run(ctx, editorial.Config{
			DB:           p.db.Writer(),
			Model:        p.gemini,
			Budgets:      p.budgetsFor(ownerID),
			ModelID:      p.editorialModel,
			OwnerID:      ownerID,
			EpisodeID:    episodeID,
			UserStem:     userStem,
			HostStem:     hostStem,
			DurationSecs: durationSecs,
			SaveRaw:      p.receiptSink(ownerID, episodeID),
		})
		if err != nil {
			return nil, err
		}
		return json.Marshal(res)
	}
}

// analysisFunc builds the analysis work for one rendered episode. The
// run locates the render itself, so the schedule carries ids only.
func (p *pipeline) analysisFunc(ownerID, episodeID string) job.Func {
	return func(ctx context.Context, progress func(job.Progress)) ([]byte, error) {
		reportEpisode(progress, ownerID, episodeID)
		res, err := analysis.Run(ctx, analysis.Config{
			DB:           p.db.Writer(),
			Batch:        p.transcriber,
			Chapters:     p.chapters,
			Budgets:      p.budgetsFor(ownerID),
			GatewayModel: p.editorialModel,
			Rates:        p.chapterRates,
			OwnerID:      ownerID,
			EpisodeID:    episodeID,
			Render:       p.locateRender,
			SaveRaw: func(ctx context.Context, name string, raw []byte) error {
				return p.receiptSink(ownerID, episodeID)(ctx, raw)
			},
		})
		if err != nil {
			return nil, err
		}
		return json.Marshal(res)
	}
}

// coverFunc builds the cover work for one episode. The run reads the
// title and notes itself, so the schedule carries ids only. A model
// failure still stores the deterministic fallback, so the episode
// ships a square cover either way.
func (p *pipeline) coverFunc(ownerID, episodeID string) job.Func {
	return func(ctx context.Context, progress func(job.Progress)) ([]byte, error) {
		reportEpisode(progress, ownerID, episodeID)
		res, err := cover.Run(ctx, cover.Config{
			DB:        p.db.Writer(),
			Model:     p.coverModel,
			Budgets:   p.budgetsFor(ownerID),
			ModelID:   p.editorialModel,
			OwnerID:   ownerID,
			EpisodeID: episodeID,
			Dir:       p.coverDir,
			SaveRaw:   p.receiptSink(ownerID, episodeID),
		})
		if err != nil {
			return nil, err
		}
		return json.Marshal(res)
	}
}

// memoryFunc builds the commitment marking work for one episode. The
// run reads the rendered words itself, so the schedule carries ids
// only.
func (p *pipeline) memoryFunc(ownerID, episodeID string) job.Func {
	return func(ctx context.Context, progress func(job.Progress)) ([]byte, error) {
		reportEpisode(progress, ownerID, episodeID)
		res, err := memory.MarkEpisode(ctx, memory.MarkConfig{
			DB:        p.db.Writer(),
			Model:     p.memoryModel,
			Budgets:   p.budgetsFor(ownerID),
			Rates:     p.markingRates,
			ModelID:   p.editorialModel,
			OwnerID:   ownerID,
			EpisodeID: episodeID,
			SaveRaw:   p.receiptSink(ownerID, episodeID),
		})
		if err != nil {
			return nil, err
		}
		return json.Marshal(res)
	}
}

// locateStems resolves both stem files for one episode with their
// alignment offsets. Blobs rest as files named by their ids under the
// media directory, so the render reads them in place through a root
// confined to that directory.
func (p *pipeline) locateStems(ctx context.Context, ownerID, episodeID string) (userPath, hostPath string, userOffsetMs, hostOffsetMs int64, err error) {
	type stem struct {
		mediaID string
		offset  int64
	}
	rows, err := p.db.Reader().QueryContext(ctx,
		`SELECT media_id, role, start_offset_ms FROM stems WHERE owner_id = ? AND episode_id = ?`,
		ownerID, episodeID)
	if err != nil {
		return "", "", 0, 0, fmt.Errorf("reprise: locate stems: %w", err)
	}
	defer rows.Close()
	found := map[string]stem{}
	for rows.Next() {
		var mediaID, role string
		var offset int64
		if err := rows.Scan(&mediaID, &role, &offset); err != nil {
			return "", "", 0, 0, fmt.Errorf("reprise: locate stems: %w", err)
		}
		found[role] = stem{mediaID: mediaID, offset: offset}
	}
	if err := rows.Err(); err != nil {
		return "", "", 0, 0, fmt.Errorf("reprise: locate stems: %w", err)
	}
	root, err := os.OpenRoot(p.mediaDir)
	if err != nil {
		return "", "", 0, 0, fmt.Errorf("reprise: locate stems: %w", err)
	}
	defer root.Close()
	pathFor := func(role string) (string, int64, error) {
		s, ok := found[role]
		if !ok || s.mediaID == "" {
			return "", 0, fmt.Errorf("reprise: locate stems: missing %s stem", role)
		}
		f, err := root.Open(s.mediaID)
		if err != nil {
			return "", 0, fmt.Errorf("reprise: locate stems: open %s stem: %w", role, err)
		}
		_ = f.Close()
		return filepath.Join(p.mediaDir, s.mediaID), s.offset, nil
	}
	userPath, userOffsetMs, err = pathFor(transcript.RoleUser)
	if err != nil {
		return "", "", 0, 0, err
	}
	hostPath, hostOffsetMs, err = pathFor(transcript.RoleHost)
	if err != nil {
		return "", "", 0, 0, err
	}
	return userPath, hostPath, userOffsetMs, hostOffsetMs, nil
}

// locateRender resolves one episode render for the analysis pass. It
// reads the latest stored render row and serves the streaming bytes
// with the probed length, so chapters land on the episode people hear.
func (p *pipeline) locateRender(ctx context.Context, ownerID, episodeID string) (analysis.RenderedFile, error) {
	var mediaID string
	err := p.db.Reader().QueryRowContext(ctx,
		`SELECT opus_media_id FROM renders WHERE owner_id = ? AND episode_id = ? ORDER BY rowid DESC LIMIT 1`,
		ownerID, episodeID).Scan(&mediaID)
	if err != nil {
		return analysis.RenderedFile{}, fmt.Errorf("reprise: locate render: %w", err)
	}
	path := filepath.Join(p.mediaDir, mediaID)
	audio, err := os.ReadFile(path)
	if err != nil {
		return analysis.RenderedFile{}, fmt.Errorf("reprise: locate render: %w", err)
	}
	length, err := ffmpeg.Duration(ctx, p.renderTools, path)
	if err != nil {
		return analysis.RenderedFile{}, fmt.Errorf("reprise: probe render: %w", err)
	}
	return analysis.RenderedFile{Audio: audio, DurationSecs: length.Seconds()}, nil
}

// jobs carries the wired long work: the runner, the render resolver,
// the reconciler, the sweeper, and the broker behind them. One value
// serves the whole process.
type jobs struct {
	runner   *job.Runner
	resolver *render.Resolver
	rec      *broker.Reconciler
	sweeper  *broker.Sweeper
	pipe     *pipeline
	broker   *broker.Broker
}

// openJobs builds the shared clients once and registers every kind on
// one durable runner. Paid kinds stay non-idempotent with no resume,
// so a restart marks them interrupted instead of paying twice. Opening
// the runner runs recovery first: interrupted paid jobs fail their
// episodes here, before any new work starts.
func openJobs(ctx context.Context, db *sqlite.DB, events *stream.Broker, loaded settings.Settings, keyed *costsqlitestore.KeyedBudget, media *mediastore.Store, renderMedia render.Media, sessionBroker *broker.Broker, diary *broker.SQLiteDiary) (*jobs, error) {
	ceiling, err := spendCeiling(loaded.DailySpendCents)
	if err != nil {
		return nil, err
	}
	credential, err := loaded.Secrets.GeminiCredential.Reveal()
	if err != nil {
		return nil, fmt.Errorf("reprise: reveal Gemini credential: %w", err)
	}
	geminiClient, err := gemini.NewClient(ctx, gemini.Config{
		Project:        loaded.VertexProject,
		Location:       loaded.VertexLocation,
		CredentialJSON: credential,
	})
	if err != nil {
		return nil, fmt.Errorf("reprise: open Gemini client: %w", err)
	}
	apiKey, err := loaded.Secrets.AssemblyAIAPIKey.Reveal()
	if err != nil {
		return nil, fmt.Errorf("reprise: reveal provider key: %w", err)
	}
	batch, err := assemblyai.NewBatchClient(assemblyai.Config{
		APIKey: apiKey,
		Model:  loaded.TranscriptionModel,
	})
	if err != nil {
		return nil, fmt.Errorf("reprise: open batch client: %w", err)
	}
	sessions, err := assemblyai.NewSessionsClient(assemblyai.SessionsBaseURL, apiKey, nil)
	if err != nil {
		return nil, fmt.Errorf("reprise: open sessions client: %w", err)
	}
	provider, err := broker.NewSessionsAdapter(sessions)
	if err != nil {
		return nil, fmt.Errorf("reprise: open sessions adapter: %w", err)
	}
	if loaded.EditorialModel == "" {
		return nil, fmt.Errorf("reprise: wire pipeline: editorial model must not be empty")
	}
	coverDir := filepath.Join(loaded.MediaDir, "covers")
	renderWorkDir := filepath.Join(loaded.MediaDir, "render-work")
	if err := os.MkdirAll(coverDir, 0o755); err != nil {
		return nil, fmt.Errorf("reprise: make cover dir: %w", err)
	}
	if err := os.MkdirAll(renderWorkDir, 0o755); err != nil {
		return nil, fmt.Errorf("reprise: make render work dir: %w", err)
	}
	pipe := newPipeline(db, geminiClient, batch, keyed, ceiling, loaded.EditorialModel, media, loaded.MediaDir, coverDir, renderWorkDir)
	resolver := &render.Resolver{
		DB:      db.Writer(),
		Media:   renderMedia,
		WorkDir: renderWorkDir,
		Locate:  pipe.locateStems,
	}
	rec, err := broker.NewReconciler(broker.ReconcilerConfig{
		DB:            db,
		Sessions:      provider,
		Artifacts:     broker.NewHTTPArtifactFetcher(nil),
		Budgets:       keyed,
		Leases:        sessionBroker.Leases(),
		Diary:         diary,
		Media:         media,
		MarginSeconds: broker.DefaultMarginSeconds,
	})
	if err != nil {
		return nil, fmt.Errorf("reprise: open reconciler: %w", err)
	}
	sweeper, err := broker.NewSweeper(broker.SweeperConfig{
		DB:            db,
		Source:        sessionBroker,
		Statuses:      provider,
		Ender:         provider,
		Reconciler:    rec,
		MarginSeconds: broker.DefaultMarginSeconds,
	})
	if err != nil {
		return nil, fmt.Errorf("reprise: open sweeper: %w", err)
	}
	store, err := jobsqlitestore.Open(ctx, jobsqlitestore.Config{DB: db})
	if err != nil {
		return nil, fmt.Errorf("reprise: open job store: %w", err)
	}
	unfinished, err := store.Unfinished(ctx)
	if err != nil {
		return nil, fmt.Errorf("reprise: list unfinished jobs: %w", err)
	}
	runner, err := job.Open(ctx, job.Config{
		Broker: events,
		Store:  store,
		Kinds:  pipe.kinds(resolver, rec, sweeper),
	})
	if err != nil {
		return nil, fmt.Errorf("reprise: open job runner: %w", err)
	}
	failInterruptedEpisodes(ctx, db, store, unfinished)
	return &jobs{runner: runner, resolver: resolver, rec: rec, sweeper: sweeper, pipe: pipe, broker: sessionBroker}, nil
}

// settleEnd wraps the session end handler: after the inner handler
// starts one reconcile job for the recorded close, the mint hold
// settles to the real connected seconds.
func (j *jobs) settleEnd(inner http.Handler) http.Handler {
	return &settleOnEnd{inner: inner, banks: j.broker, starter: j.runner, rec: j.rec}
}

// settleOnEnd wraps the session end handler: after the inner handler
// records the provider close, it starts one reconcile job for the
// session. The reconciler settles the mint hold to the real connected
// seconds exactly once, so each mint releases and the daily ceiling
// stops leaking. A repeat end starts a repeat job, and the claim
// inside settles money only on the first pass.
type settleOnEnd struct {
	inner   http.Handler
	banks   *broker.Broker
	starter jobStarter
	rec     *broker.Reconciler
}

// jobStarter starts one named job kind. The job runner implements it.
type jobStarter interface {
	// StartKind runs fn as a job of the named kind.
	StartKind(ctx context.Context, kind string, fn job.Func) (string, error)
}

// statusWriter records the status the inner handler answers, so the
// wrapper settles only recorded closes.
type statusWriter struct {
	http.ResponseWriter
	status int
}

// WriteHeader records the status before answering.
func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

// ServeHTTP records the close first, then settles it. A refused end
// settles nothing, and a close the end never recorded waits for the
// sweep instead of pricing silence.
func (s *settleOnEnd) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rec := &statusWriter{ResponseWriter: w, status: http.StatusOK}
	s.inner.ServeHTTP(rec, r)
	if r.Method != http.MethodPost || rec.status != http.StatusOK {
		return
	}
	sessionID := r.PathValue("id")
	if sessionID == "" {
		return
	}
	in, err := s.banks.SettleInput(r.Context(), sessionID)
	if errors.Is(err, broker.ErrNoClose) || errors.Is(err, broker.ErrUnknownSession) {
		return
	}
	if err != nil {
		log.Printf("reprise settle: input for %s: %v", sessionID, err)
		return
	}
	if _, err := s.starter.StartKind(r.Context(), broker.KindName, s.rec.RunFunc(in)); err != nil {
		log.Printf("reprise settle: start reconcile for %s: %v", sessionID, err)
	}
}

// runSweepLoop starts one sweep per interval until ctx ends. Each run
// settles sessions the end path never reached and deletes their
// provider records after, so abandoned mints release too. An immediate
// run first catches sessions a restart left behind.
func (j *jobs) runSweepLoop(ctx context.Context) {
	j.startSweep(ctx)
	tick := time.NewTicker(sweepInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			j.startSweep(ctx)
		}
	}
}

// startSweep starts one sweep job. A start failure logs, and the next
// tick retries, so a full runner never stops the schedule.
func (j *jobs) startSweep(ctx context.Context) {
	if _, err := j.runner.StartKind(ctx, broker.SweepKindName,
		j.sweeper.RunFunc(broker.SweepInput{MarginSeconds: -1})); err != nil {
		log.Printf("reprise sweep: start: %v", err)
	}
}

// failInterruptedEpisodes fails the episodes a restart left with
// unfinished paid jobs. Recovery already marked those jobs
// interrupted; the episode waits in failed for an explicit retry, so
// no paid call reruns silently. Idempotent kinds resume instead and
// keep their episodes. Records without an episode linkage log for
// review instead of failing blindly.
func failInterruptedEpisodes(ctx context.Context, db *sqlite.DB, store job.Store, unfinished []job.Record) {
	for _, rec := range unfinished {
		if !paidKinds[rec.Kind] {
			continue
		}
		current, err := store.Get(ctx, rec.ID)
		if err != nil {
			log.Printf("reprise restart: read job %s: %v", rec.ID, err)
			continue
		}
		if current.Status != job.StatusInterrupted {
			continue
		}
		var desc episodeDescriptor
		if err := json.Unmarshal(current.Progress.Detail, &desc); err != nil || desc.EpisodeID == "" {
			log.Printf("reprise restart: job %s carries no episode, leaving it for review", rec.ID)
			continue
		}
		if err := episode.MarkInterrupted(ctx, db, desc.EpisodeID); err != nil {
			log.Printf("reprise restart: fail episode %s: %v", desc.EpisodeID, err)
		}
	}
}
