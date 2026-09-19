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
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"math"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/nrynss/keel/cost"
	costsqlitestore "github.com/nrynss/keel/cost/sqlitestore"
	flagsqlitestore "github.com/nrynss/keel/flag/sqlitestore"
	"github.com/nrynss/keel/gate"
	"github.com/nrynss/keel/id"
	"github.com/nrynss/keel/lease"
	leasesqlitestore "github.com/nrynss/keel/lease/sqlitestore"
	"github.com/nrynss/keel/mediastore"
	mediasqlitestore "github.com/nrynss/keel/mediastore/sqlitestore"
	"github.com/nrynss/keel/sqlite"
	"github.com/nrynss/keel/stream"
	"github.com/nrynss/keel/upload"
	"github.com/nrynss/keel/wire"
	"github.com/nrynss/reprise/internal/api"
	"github.com/nrynss/reprise/internal/assemblyai"
	"github.com/nrynss/reprise/internal/broker"
	"github.com/nrynss/reprise/internal/host"
	"github.com/nrynss/reprise/internal/identity"
	"github.com/nrynss/reprise/internal/limits"
	"github.com/nrynss/reprise/internal/settings"
	reprisestore "github.com/nrynss/reprise/internal/store"
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
// leaves out.
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
	if err := wireAPI(context.Background(), mux, loaded); err != nil {
		log.Fatalf("wire routes: %v", err)
	}
	mux.Handle("/", appHandler(*webDir))
	gate.handler = mux

	srv := &http.Server{Addr: *addr, Handler: gate}

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sigs)

	log.Printf("reprise listening on %s, serving %s", *addr, *webDir)
	if err := run(srv, gate, nil, drainBudget(loaded), sigs); err != nil {
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
// a missing secret does.
//
// One owner may spend up to the whole daily ceiling. The per-owner ceiling
// still isolates accounting and lets the admin page lower one owner, while
// the global ceiling caps the day across owners. Both constructors take
// this one value, and the mount refuses to wire them if they ever disagree.
func wireAPI(ctx context.Context, mux *http.ServeMux, loaded settings.Settings) error {
	signingKey, err := loaded.Secrets.SessionSigningKey.Reveal()
	if err != nil {
		return fmt.Errorf("reprise: reveal session signing key: %w", err)
	}
	apiKey, err := loaded.Secrets.AssemblyAIAPIKey.Reveal()
	if err != nil {
		return fmt.Errorf("reprise: reveal provider key: %w", err)
	}
	ceiling, err := spendCeiling(loaded.DailySpendCents)
	if err != nil {
		return err
	}
	db, err := sqlite.Open(ctx, sqlite.Config{Path: filepath.Join(loaded.DataDir, "reprise.db")})
	if err != nil {
		return fmt.Errorf("reprise: open database: %w", err)
	}
	wired := false
	defer func() {
		if !wired {
			_ = db.Close()
		}
	}()
	if _, err := reprisestore.Open(ctx, db); err != nil {
		return fmt.Errorf("reprise: migrate diary schema: %w", err)
	}
	identitySvc, err := identity.New(ctx, identity.Config{DB: db, SigningKey: signingKey})
	if err != nil {
		return fmt.Errorf("reprise: open guest sessions: %w", err)
	}
	costStore, err := costsqlitestore.Open(ctx, costsqlitestore.Config{DB: db, Limit: ceiling})
	if err != nil {
		return fmt.Errorf("reprise: open spend ledger: %w", err)
	}
	keyed := costsqlitestore.NewKeyedBudget(costStore)
	flagStore, err := flagsqlitestore.Open(ctx, flagsqlitestore.Config{DB: db})
	if err != nil {
		return fmt.Errorf("reprise: open runtime switches: %w", err)
	}
	quota, err := lease.NewQuota(concurrentSessionSlots)
	if err != nil {
		return fmt.Errorf("reprise: size session quota: %w", err)
	}
	leaseStore, err := leasesqlitestore.Open(ctx, leasesqlitestore.Config{DB: db})
	if err != nil {
		return fmt.Errorf("reprise: open session leases: %w", err)
	}
	minter, err := assemblyai.NewClient(assemblyai.TokenBaseURL, apiKey, nil)
	if err != nil {
		return fmt.Errorf("reprise: open token client: %w", err)
	}
	diary, err := broker.NewSQLiteDiary(db)
	if err != nil {
		return fmt.Errorf("reprise: open session diary: %w", err)
	}
	sessionBroker, err := broker.New(broker.Config{
		Flags:             flagStore,
		Budgets:           keyed,
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
		return fmt.Errorf("reprise: open session broker: %w", err)
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
		return fmt.Errorf("reprise: open guest limits: %w", err)
	}
	mediaIndex, err := mediasqlitestore.Open(ctx, mediasqlitestore.Config{DB: db})
	if err != nil {
		return fmt.Errorf("reprise: open media index: %w", err)
	}
	mediaStore, err := mediastore.Open(ctx, mediastore.Config{
		Dir:          loaded.MediaDir,
		Index:        mediaIndex,
		ContentTypes: mediaContentTypes,
		Authorize:    identitySvc.AuthorizeMedia,
	})
	if err != nil {
		return fmt.Errorf("reprise: open media store: %w", err)
	}
	uploadHandler, err := upload.New(upload.Config{
		Dir:      filepath.Join(loaded.MediaDir, "upload-stage"),
		Store:    mediaStore,
		BasePath: api.UploadBasePath,
	})
	if err != nil {
		return fmt.Errorf("reprise: open upload handler: %w", err)
	}
	spendGate, err := gate.New(gate.Config{})
	if err != nil {
		return fmt.Errorf("reprise: open spend gate: %w", err)
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
		Admin:             adminSvc.Handler(),
		Uploads:           uploadHandler,
		Media:             mediaStore,
		Events:            stream.New(stream.Config{}),
		OwnerSessionLimit: ceiling,
		OwnerDefaultLimit: ceiling,
	}); err != nil {
		return fmt.Errorf("reprise: mount routes: %w", err)
	}
	uploadHandler.Start()
	wired = true
	return nil
}
