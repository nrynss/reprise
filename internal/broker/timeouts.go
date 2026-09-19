package broker

import "time"

// Session path bounds under a slow provider.
//
// Live measurement showed the provider answering near 50 seconds on a
// greeting reply.done against a 4 second baseline, with healthy transport
// (session.ready near 2 seconds) and clean teardown. The path below
// assumes one host reply may take 60 seconds or more from here on.
//
// Every constant names one bound and its reason. Call sites still carry
// their own literals today, so a later change swaps each literal for the
// constant named beside it. Nothing here changes fast path behavior. It
// only pins the floors a slow provider needs.
const (
	// AssumedReplyLatency is the slowest host reply the session path
	// plans for. Recorded replies landed near 52 seconds, so the plan
	// rounds up to a full minute.
	AssumedReplyLatency = 60 * time.Second

	// MinReplyWait is the shortest wait any reply give-up may keep. It
	// clears the assumed latency with room for transport skew, so a
	// healthy but slow reply never reads as a failure.
	MinReplyWait = 90 * time.Second

	// ReconcileMaxAttempts caps the reconcile job attempts. Reads repeat
	// safely and the claim guard keeps money at most once, so three tries
	// ride through transient provider errors without live locking.
	ReconcileMaxAttempts = 3

	// SweepMaxAttempts caps the sweep job attempts. Ending repeats safely
	// and settling runs at most once through the reconciler, so three
	// tries carry the same reasoning as the reconcile kind.
	SweepMaxAttempts = 3

	// OverCapMarginSeconds is the grace past the session cap before a
	// session counts as over cap. It absorbs clock skew and settle lag
	// between the browser end and the provider close. It stays far below
	// any session cap, so abandonment still surfaces promptly.
	OverCapMarginSeconds = 60
)
