// Command reprise serves the built web application and a health endpoint.
// It is one static binary for one distroless container, so it reads the app
// from disk instead of embedding it: the Docker build stages web/build next
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
	"strings"

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
// Unknown paths return index.html so client-side routes survive a reload.
func appHandler(webDir string) http.Handler {
	root := http.FileServer(http.Dir(webDir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			if _, err := os.Stat(webDir + r.URL.Path); errors.Is(err, fs.ErrNotExist) {
				r2 := r.Clone(r.Context())
				r2.URL.Path = "/"
				r = r2
			}
		}
		if strings.HasSuffix(r.URL.Path, "/") {
			w.Header().Set("Cache-Control", "no-cache")
		}
		root.ServeHTTP(w, r)
	})
}
