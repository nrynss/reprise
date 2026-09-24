package render

import (
	"errors"
	"strings"
	"testing"
)

// TestKindOfFollowsConcurrency registers the render kind with room for
// exactly the concurrency it receives, so the setting decides how many
// renders run at once.
func TestKindOfFollowsConcurrency(t *testing.T) {
	for _, concurrency := range []int{1, 2, 4} {
		kind, err := KindOf(&Resolver{}, concurrency)
		if err != nil {
			t.Fatalf("KindOf(%d): %v", concurrency, err)
		}
		if kind.Limit != concurrency {
			t.Fatalf("KindOf(%d) limit = %d, want %d", concurrency, kind.Limit, concurrency)
		}
	}
}

// TestKindOfCarriesResumeAttempts pins that the kind alone leaves room
// for a resumed attempt. A restart resumes the render without the binary
// patching the kind.
func TestKindOfCarriesResumeAttempts(t *testing.T) {
	kind, err := KindOf(&Resolver{}, 1)
	if err != nil {
		t.Fatalf("KindOf: %v", err)
	}
	if !kind.Idempotent {
		t.Fatal("render kind is not idempotent, so a restart never resumes it")
	}
	if kind.Resume == nil {
		t.Fatal("render kind carries no resume work")
	}
	if kind.MaxAttempts < 2 {
		t.Fatalf("render MaxAttempts = %d, want room for a resumed attempt", kind.MaxAttempts)
	}
}

// TestKindOfRefusesConcurrencyBelowOne pins that a concurrency with no
// room for a render refuses by naming the setting.
func TestKindOfRefusesConcurrencyBelowOne(t *testing.T) {
	for _, concurrency := range []int{0, -1} {
		_, err := KindOf(&Resolver{}, concurrency)
		if !errors.Is(err, ErrConcurrency) {
			t.Fatalf("KindOf(%d) error = %v, want the concurrency refusal", concurrency, err)
		}
		if !strings.Contains(err.Error(), "render_concurrency") {
			t.Fatalf("KindOf(%d) error = %v, want the setting name", concurrency, err)
		}
	}
}
