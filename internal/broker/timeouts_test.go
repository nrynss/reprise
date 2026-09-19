package broker

import (
	"testing"
	"time"
)

// TestSlowProviderFloors pins the session path bounds against a slow
// provider. It asserts values only, never timing, so it runs the same on
// any clock. Lowering a floor below the recorded reply latency fails here
// first, before any live session could feel it.
func TestSlowProviderFloors(t *testing.T) {
	t.Parallel()
	if AssumedReplyLatency < time.Minute {
		t.Fatalf("assumed reply latency %v sits below one minute", AssumedReplyLatency)
	}
	if MinReplyWait < AssumedReplyLatency+30*time.Second {
		t.Fatalf("minimum reply wait %v leaves no headroom past assumed latency %v", MinReplyWait, AssumedReplyLatency)
	}
	if ReconcileMaxAttempts < 3 {
		t.Fatalf("reconcile attempts %d leave no room for a transient read retry", ReconcileMaxAttempts)
	}
	if SweepMaxAttempts < 3 {
		t.Fatalf("sweep attempts %d leave no room for a transient status retry", SweepMaxAttempts)
	}
	if OverCapMarginSeconds < 60 {
		t.Fatalf("over cap margin %d seconds sits below one minute of skew room", OverCapMarginSeconds)
	}
}
