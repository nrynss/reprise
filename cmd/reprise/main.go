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
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/nrynss/keel/id"
	"github.com/nrynss/keel/wire"
	"github.com/nrynss/reprise/internal/settings"
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
