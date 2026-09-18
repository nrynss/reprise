// Package api registers the HTTP routes the product serves. Every handler
// answers through the shared error envelope. Screens branch on stable codes
// and never on wording. Stubs refuse until their own change lands.
package api

import (
	"net/http"

	"github.com/nrynss/keel/wire"
)

// CodeNotImplemented is the envelope code every stub handler answers with.
// Screens branch on it to explain that a control has no handler yet.
const CodeNotImplemented = "not_implemented"

// Route names one entry of the route table. Method follows the ServeMux
// syntax. An empty method matches every method, which suits a subtree such
// as the upload prefix. Pattern is the ServeMux pattern Register mounts.
type Route struct {
	// Method is the HTTP method, or empty for every method.
	Method string `json:"method"`
	// Pattern is the ServeMux pattern, with wildcards where ids sit.
	Pattern string `json:"pattern"`
}

// routeTable lists every route before handlers exist. Human pages are
// singular. API routes are plural and live under /api. The chunked upload
// handler takes the upload prefix later, mounted at its base path. The job
// stream takes the events route later, serving one job topic. The blob
// store takes the media route later, behind the ownership check.
var routeTable = []Route{
	{Method: "POST", Pattern: "/api/sessions"},
	{Method: "POST", Pattern: "/api/sessions/{id}/end"},
	{Method: "", Pattern: "/api/uploads/"},
	{Method: "GET", Pattern: "/api/episodes"},
	{Method: "GET", Pattern: "/api/episodes/{id}"},
	{Method: "POST", Pattern: "/api/episodes/{id}/decisions"},
	{Method: "POST", Pattern: "/api/episodes/{id}/done"},
	{Method: "GET", Pattern: "/api/jobs/{id}/events"},
	{Method: "POST", Pattern: "/api/episodes/{id}/publish"},
	{Method: "DELETE", Pattern: "/api/episodes/{id}/publish"},
	{Method: "DELETE", Pattern: "/api/episodes/{id}"},
	{Method: "GET", Pattern: "/api/threads"},
	{Method: "GET", Pattern: "/media/{id}"},
}

// Routes returns a copy of the route table. Tests and docs read the same
// list Register mounts, so drift between the two fails instead of hiding.
func Routes() []Route {
	out := make([]Route, len(routeTable))
	copy(out, routeTable)
	return out
}

// Register mounts every route on mux with its stub handler. Later changes
// replace stubs with real handlers and hand them the stores they need.
func Register(mux *http.ServeMux) {
	for _, route := range routeTable {
		pattern := route.Pattern
		if route.Method != "" {
			pattern = route.Method + " " + route.Pattern
		}
		mux.HandleFunc(pattern, handleStub)
	}
}

// handleStub refuses every request until its real handler lands. It answers
// through the shared envelope, so the browser parses it like any refusal.
func handleStub(w http.ResponseWriter, _ *http.Request) {
	_ = wire.WriteError(w, http.StatusNotImplemented, CodeNotImplemented, "this control has no handler yet", nil)
}
