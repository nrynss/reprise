// Package api registers the HTTP routes the product serves. Every handler
// answers through the shared error envelope. Screens branch on stable codes
// and never on wording. Stubs refuse until their own change lands. Mounted
// routes run behind the spend gate outermost, then the guest session
// middleware, then the handler, so a refused request never mints a guest
// row and never reaches a handler.
package api

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/nrynss/keel/cost"
	"github.com/nrynss/keel/gate"
	"github.com/nrynss/keel/job"
	"github.com/nrynss/keel/stream"
	"github.com/nrynss/keel/wire"
	"github.com/nrynss/reprise/internal/limits"
)

// CodeNotImplemented is the envelope code every stub handler answers with.
// Screens branch on it to explain that a control has no handler yet.
const CodeNotImplemented = "not_implemented"

// UploadBasePath is the URL prefix the chunked upload handler serves under.
// The process passes it to the upload handler config, so the handler and
// the table below agree on one literal.
const UploadBasePath = "/api/uploads"

// ErrMount reports dependencies Mount cannot wire. Either a handler is
// present without the gate or the guest middleware around it, or the two
// owner ceilings disagree.
var ErrMount = errors.New("api: cannot mount routes")

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
// store takes the media route later, behind the ownership check. The admin
// trio takes the limits handler, behind the owner proof.
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
	{Method: "GET", Pattern: limits.PatternLimits},
	{Method: "POST", Pattern: limits.PatternPause},
	{Method: "POST", Pattern: limits.PatternOwnerLimit},
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

// privacyPatterns names the publish, revoke, and erase routes the
// privacy feature mounts with its real handlers. Mount leaves them
// alone, so the feature hook registers them without a duplicate
// pattern panic. The table still lists them, so the goldens and the
// browser mirror keep matching the served surface.
var privacyPatterns = map[string]bool{
	"POST /api/episodes/{id}/publish":   true,
	"DELETE /api/episodes/{id}/publish": true,
	"DELETE /api/episodes/{id}":         true,
}

// GuestSessions resolves the guest behind each request. The identity
// service implements it. Mount wraps every implemented handler in it,
// inside the spend gate.
type GuestSessions interface {
	// Middleware ensures the request carries a guest user downstream.
	Middleware(http.Handler) http.Handler
}

// Dependencies carries the implemented handlers Mount wires. A nil handler
// leaves its route on the stub, except the privacy routes the feature
// hook mounts, which Mount leaves unregistered. A process without that
// store still serves the table. Sessions is the session broker. Episodes serves the
// episode list, detail, decisions, and mark done routes. SessionEnd
// records the provider close the browser already sent. Threads serves the
// cross episode index. Admin is the limits handler. Uploads is the chunked
// upload handler, configured with UploadBasePath. Media is the blob store.
// Events is the stream broker serving one job topic per request. The two
// owner ceilings must agree whenever Sessions or Admin is present, because
// the broker provisions one and the spend view subtracts from the other.
type Dependencies struct {
	// Gate refuses over-limit callers before any guest row is minted.
	Gate *gate.Gate
	// Rule is the spend budget every mounted route draws on.
	Rule gate.Rule
	// Identity resolves the guest behind each request.
	Identity GuestSessions
	// Sessions starts live sessions.
	Sessions http.Handler
	// Episodes serves the episode list, detail, decisions, and done.
	Episodes http.Handler
	// SessionEnd records the provider close on a diary session.
	SessionEnd http.Handler
	// Threads serves the cross episode index.
	Threads http.Handler
	// Admin answers the caps, the switch, and spend.
	Admin http.Handler
	// Uploads receives stem chunks.
	Uploads http.Handler
	// Media serves private blobs.
	Media http.Handler
	// Events streams one job over server-sent events.
	Events *stream.Broker
	// OwnerSessionLimit is the ceiling the broker provisions on first mint.
	OwnerSessionLimit cost.Price
	// OwnerDefaultLimit is the ceiling the spend view subtracts from.
	OwnerDefaultLimit cost.Price
}

// Mount wires every implemented handler on mux behind the spend gate
// outermost, then the guest session middleware, then the handler. Routes
// with no handler stay on the stub, except the privacy routes, which
// stay unregistered for the feature hook. It returns an error when the two owner
// ceilings disagree, when a handler arrives without the gate or the guest
// middleware around it, or when the rule cannot protect a route.
func Mount(mux *http.ServeMux, deps Dependencies) error {
	if deps.Sessions != nil || deps.Admin != nil {
		if deps.OwnerSessionLimit != deps.OwnerDefaultLimit {
			return fmt.Errorf("api: mount: %w: session limit %d disagrees with admin default %d",
				ErrMount, deps.OwnerSessionLimit, deps.OwnerDefaultLimit)
		}
	}
	for _, route := range routeTable {
		next := deps.handlerFor(route)
		if next == nil {
			if privacyPatterns[route.Method+" "+route.Pattern] {
				continue
			}
			pattern := route.Pattern
			if route.Method != "" {
				pattern = route.Method + " " + route.Pattern
			}
			mux.HandleFunc(pattern, handleStub)
			continue
		}
		if deps.Gate == nil {
			return fmt.Errorf("api: mount %s: %w: gate must not be nil beside an implemented handler",
				route.Pattern, ErrMount)
		}
		if deps.Identity == nil {
			return fmt.Errorf("api: mount %s: %w: guest middleware must not be nil beside an implemented handler",
				route.Pattern, ErrMount)
		}
		protected, err := deps.Gate.Protect(deps.Rule, deps.Identity.Middleware(next))
		if err != nil {
			return fmt.Errorf("api: mount %s: %w", route.Pattern, err)
		}
		pattern := route.Pattern
		if route.Method != "" {
			pattern = route.Method + " " + route.Pattern
		}
		mux.Handle(pattern, protected)
		if route.Pattern == UploadBasePath+"/" {
			mux.Handle(UploadBasePath, protected)
		}
	}
	return nil
}

// handlerFor returns the implemented handler for one table entry, or nil
// when the route stays on the stub. It matches on method and pattern
// together, so a wired GET never serves its sibling DELETE. The admin
// handler answers all three admin patterns through its own mux. The upload
// handler takes the subtree Mount registers under both the base path and
// its slash form, mirroring the upload package mount.
func (d Dependencies) handlerFor(route Route) http.Handler {
	switch route.Method + " " + route.Pattern {
	case "POST /api/sessions":
		return d.Sessions
	case "POST /api/sessions/{id}/end":
		return d.SessionEnd
	case "GET /api/episodes", "GET /api/episodes/{id}",
		"POST /api/episodes/{id}/decisions", "POST /api/episodes/{id}/done":
		return d.Episodes
	case "GET /api/threads":
		return d.Threads
	case "GET " + limits.PatternLimits,
		"POST " + limits.PatternPause,
		"POST " + limits.PatternOwnerLimit:
		return d.Admin
	case " " + UploadBasePath + "/":
		return d.Uploads
	case "GET /media/{id}":
		return d.Media
	case "GET /api/jobs/{id}/events":
		return d.jobEvents()
	default:
		return nil
	}
}

// jobEvents streams one job topic over server-sent events. The topic comes
// from the job id in the path, through the shared topic function the job
// runner publishes on. A nil broker leaves the route on the stub.
func (d Dependencies) jobEvents() http.Handler {
	if d.Events == nil {
		return nil
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d.Events.ServeTopic(w, r, job.Topic(r.PathValue("id")))
	})
}

// handleStub refuses every request until its real handler lands. It answers
// through the shared envelope, so the browser parses it like any refusal.
func handleStub(w http.ResponseWriter, _ *http.Request) {
	_ = wire.WriteError(w, http.StatusNotImplemented, CodeNotImplemented, "this control has no handler yet", nil)
}
