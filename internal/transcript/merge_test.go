package transcript_test

import (
	"errors"
	"testing"

	"github.com/nrynss/reprise/internal/assemblyai"
	"github.com/nrynss/reprise/internal/transcript"
)

// userWords builds provider words from start and end pairs.
func userWords(t *testing.T, marks ...any) []assemblyai.Word {
	t.Helper()
	if len(marks)%3 != 0 {
		t.Fatalf("marks need text, start, and end triples")
	}
	var out []assemblyai.Word
	for i := 0; i < len(marks); i += 3 {
		text, _ := marks[i].(string)
		start, _ := marks[i+1].(int64)
		end, _ := marks[i+2].(int64)
		out = append(out, assemblyai.Word{Text: text, StartMs: start, EndMs: end, Confidence: 0.99})
	}
	return out
}

// checkTimeline asserts the merged texts and spans match exactly.
func checkTimeline(t *testing.T, got []transcript.Word, want []transcript.Word) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("merged %d words, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i].Text != want[i].Text || got[i].StartMs != want[i].StartMs ||
			got[i].EndMs != want[i].EndMs || got[i].Role != want[i].Role {
			t.Fatalf("word %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestMergeShiftsBothSides pins user and host words on the episode clock
// with per-stem offsets applied.
func TestMergeShiftsBothSides(t *testing.T) {
	t.Parallel()
	user := userWords(t, "hello", int64(100), int64(200), "world", int64(300), int64(400))
	host := []transcript.HostReply{{
		StartMs: 1000,
		Words:   []transcript.HostWord{{Text: "hi", StartMs: 0, EndMs: 150}},
	}}
	got, err := transcript.Merge(user, host, transcript.Offsets{UserMs: 50, HostMs: 25})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	checkTimeline(t, got, []transcript.Word{
		{Text: "hello", StartMs: 150, EndMs: 250, Role: transcript.RoleUser},
		{Text: "world", StartMs: 350, EndMs: 450, Role: transcript.RoleUser},
		{Text: "hi", StartMs: 1025, EndMs: 1175, Role: transcript.RoleHost},
	})
}

// TestMergeInterleavesByStart places a host reply between two user words
// when its episode time falls between them.
func TestMergeInterleavesByStart(t *testing.T) {
	t.Parallel()
	user := userWords(t, "first", int64(0), int64(100), "last", int64(900), int64(1000))
	host := []transcript.HostReply{{
		StartMs: 400,
		Words:   []transcript.HostWord{{Text: "middle", StartMs: 0, EndMs: 120}},
	}}
	got, err := transcript.Merge(user, host, transcript.Offsets{})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	checkTimeline(t, got, []transcript.Word{
		{Text: "first", StartMs: 0, EndMs: 100, Role: transcript.RoleUser},
		{Text: "middle", StartMs: 400, EndMs: 520, Role: transcript.RoleHost},
		{Text: "last", StartMs: 900, EndMs: 1000, Role: transcript.RoleUser},
	})
}

// TestMergeBreaksTiesWithUserFirst keeps words sharing one start in a
// fixed order with the user ahead of the host.
func TestMergeBreaksTiesWithUserFirst(t *testing.T) {
	t.Parallel()
	user := userWords(t, "yours", int64(500), int64(600))
	host := []transcript.HostReply{{
		StartMs: 500,
		Words:   []transcript.HostWord{{Text: "mine", StartMs: 0, EndMs: 90}},
	}}
	got, err := transcript.Merge(user, host, transcript.Offsets{})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	checkTimeline(t, got, []transcript.Word{
		{Text: "yours", StartMs: 500, EndMs: 600, Role: transcript.RoleUser},
		{Text: "mine", StartMs: 500, EndMs: 590, Role: transcript.RoleHost},
	})
}

// TestMergeOffsetsEachReplySeparately pins two replies whose starts shift
// their words by different amounts.
func TestMergeOffsetsEachReplySeparately(t *testing.T) {
	t.Parallel()
	host := []transcript.HostReply{
		{StartMs: 0, Words: []transcript.HostWord{{Text: "open", StartMs: 10, EndMs: 60}}},
		{StartMs: 2000, Words: []transcript.HostWord{{Text: "close", StartMs: 20, EndMs: 80}}},
	}
	got, err := transcript.Merge(nil, host, transcript.Offsets{HostMs: 100})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	checkTimeline(t, got, []transcript.Word{
		{Text: "open", StartMs: 110, EndMs: 160, Role: transcript.RoleHost},
		{Text: "close", StartMs: 2120, EndMs: 2180, Role: transcript.RoleHost},
	})
}

// TestMergeRejectsInvertedSpan checks a word ending before it starts
// returns the order sentinel with nothing merged.
func TestMergeRejectsInvertedSpan(t *testing.T) {
	t.Parallel()
	user := userWords(t, "broken", int64(300), int64(200))
	merged, err := transcript.Merge(user, nil, transcript.Offsets{})
	if !errors.Is(err, transcript.ErrOrder) {
		t.Fatalf("merge error = %v, want ErrOrder", err)
	}
	if merged != nil {
		t.Fatalf("merged = %+v, want nothing beside the error", merged)
	}
}

// TestMergeEmpty merges nothing into an empty timeline without error.
func TestMergeEmpty(t *testing.T) {
	t.Parallel()
	merged, err := transcript.Merge(nil, nil, transcript.Offsets{})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if len(merged) != 0 {
		t.Fatalf("merged = %+v, want empty", merged)
	}
}
