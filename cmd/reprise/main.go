// Command reprise serves the built web application and a health endpoint.
// It is one static binary for one distroless container, so it reads the app
// from disk instead of embedding it. The Docker build stages web/build next
// to the binary and the container needs no rebuild to ship a web change.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/nrynss/keel/id"
)

// bootID names this process instance in the health response, so an operator
// can tell a fresh container from one that survived a restart.
var bootID string

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	webDir := flag.String("web", "web/build", "directory holding the built web application")
	flag.Parse()

	boot, err := id.New()
	if err != nil {
		log.Fatalf("generate boot id: %v", err)
	}
	bootID = boot

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handleHealth)
	mux.Handle("/", appHandler(*webDir))

	log.Printf("reprise listening on %s, serving %s", *addr, *webDir)
	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatalf("listen: %v", err)
	}
}

// handleHealth answers GET /healthz with 200. The body names the process so a
// probe against the wrong container is visible in the response itself.
func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintf(w, "ok %s\n", bootID)
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
