package render

import (
	"strings"
	"testing"
)

func equalRanges(a, b []RangeMs) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestMergeRangesJoinsTouching(t *testing.T) {
	got := MergeRanges([]RangeMs{{Start: 0, End: 100}, {Start: 100, End: 200}, {Start: 300, End: 400}})
	want := []RangeMs{{Start: 0, End: 200}, {Start: 300, End: 400}}
	if !equalRanges(got, want) {
		t.Fatalf("merge touching: got %+v want %+v", got, want)
	}
}

func TestMergeRangesJoinsOverlapUnsorted(t *testing.T) {
	in := []RangeMs{{Start: 500, End: 700}, {Start: 0, End: 100}, {Start: 50, End: 600}}
	got := MergeRanges(in)
	want := []RangeMs{{Start: 0, End: 700}}
	if !equalRanges(got, want) {
		t.Fatalf("merge overlap: got %+v want %+v", got, want)
	}
	if len(in) != 3 || in[0] != (RangeMs{Start: 500, End: 700}) {
		t.Fatalf("merge modified its input: %+v", in)
	}
}

func TestKeepRangesComplementsCuts(t *testing.T) {
	got := KeepRanges(1000, []RangeMs{{Start: 200, End: 400}, {Start: 600, End: 800}})
	want := []RangeMs{{Start: 0, End: 200}, {Start: 400, End: 600}, {Start: 800, End: 1000}}
	if !equalRanges(got, want) {
		t.Fatalf("keep: got %+v want %+v", got, want)
	}
}

func TestKeepRangesClipsOutsideCuts(t *testing.T) {
	got := KeepRanges(1000, []RangeMs{{Start: -500, End: 100}, {Start: 900, End: 5000}})
	want := []RangeMs{{Start: 100, End: 900}}
	if !equalRanges(got, want) {
		t.Fatalf("keep clipped: got %+v want %+v", got, want)
	}
}

func TestKeepRangesEmptyWhenAllCut(t *testing.T) {
	if got := KeepRanges(1000, []RangeMs{{Start: 0, End: 1000}}); len(got) != 0 {
		t.Fatalf("keep all cut: got %+v", got)
	}
	if got := KeepRanges(0, nil); len(got) != 0 {
		t.Fatalf("keep zero duration: got %+v", got)
	}
}

func TestIntersectRangesKeepsOverlapOnly(t *testing.T) {
	kept := []RangeMs{{Start: 0, End: 200}, {Start: 400, End: 900}}
	got := IntersectRanges(RangeMs{Start: 100, End: 500}, kept)
	want := []RangeMs{{Start: 100, End: 200}, {Start: 400, End: 500}}
	if !equalRanges(got, want) {
		t.Fatalf("intersect: got %+v want %+v", got, want)
	}
}

func TestJoinFadesShortNeighbourCuts(t *testing.T) {
	kept := []RangeMs{{Start: 0, End: 2000}, {Start: 2000, End: 4000}, {Start: 4000, End: 4005}, {Start: 4005, End: 8000}}
	fades := JoinFades(kept)
	if len(fades) != 3 {
		t.Fatalf("fades length: got %d", len(fades))
	}
	if fades[0].Milliseconds() != Crossfade.Milliseconds() {
		t.Fatalf("long neighbours should fade: got %v", fades[0])
	}
	if fades[1].Milliseconds() != 0 || fades[2].Milliseconds() != 0 {
		t.Fatalf("joins at the short range should cut: got %v", fades)
	}
}

func TestHashInputsStableAcrossCutOrder(t *testing.T) {
	base := Input{
		UserSHA256:   [32]byte{1},
		HostSHA256:   [32]byte{2},
		UserOffsetMs: 120,
		HostOffsetMs: 340,
		Cuts:         []RangeMs{{Start: 0, End: 100}, {Start: 200, End: 300}},
		ColdOpen:     &RangeMs{Start: 400, End: 900},
		DurationMs:   1000,
	}
	swapped := base
	swapped.Cuts = []RangeMs{{Start: 200, End: 300}, {Start: 0, End: 100}}
	if HashInputs(base) != HashInputs(swapped) {
		t.Fatalf("hash moves with cut order")
	}
	changed := base
	changed.HostOffsetMs = 341
	if HashInputs(base) == HashInputs(changed) {
		t.Fatalf("hash ignores the host offset")
	}
	changed = base
	changed.ColdOpen = nil
	if HashInputs(base) == HashInputs(changed) {
		t.Fatalf("hash ignores the cold open")
	}
	if h := HashInputs(base); len(h) != 64 {
		t.Fatalf("hash length: got %d", len(h))
	}
}

func TestHashInputsVariesWithAssemblyParams(t *testing.T) {
	base := Input{
		UserSHA256:   [32]byte{1},
		HostSHA256:   [32]byte{2},
		UserOffsetMs: 120,
		HostOffsetMs: 340,
		Cuts:         []RangeMs{{Start: 0, End: 100}},
		ColdOpen:     &RangeMs{Start: 400, End: 900},
		DurationMs:   1000,
		CrossfadeMs:  10,
		MixRateHz:    48000,
		OpusBitrate:  "96k",
		AACBitrate:   "128k",
	}
	changed := base
	changed.CrossfadeMs = 20
	if HashInputs(base) == HashInputs(changed) {
		t.Fatalf("hash ignores the crossfade")
	}
	changed = base
	changed.MixRateHz = 44100
	if HashInputs(base) == HashInputs(changed) {
		t.Fatalf("hash ignores the mix rate")
	}
	changed = base
	changed.OpusBitrate = "64k"
	if HashInputs(base) == HashInputs(changed) {
		t.Fatalf("hash ignores the opus bitrate")
	}
	changed = base
	changed.AACBitrate = "96k"
	if HashInputs(base) == HashInputs(changed) {
		t.Fatalf("hash ignores the aac bitrate")
	}
}

func TestSecondsFormatsMicroseconds(t *testing.T) {
	if got := seconds(1500); got != "1.500000" {
		t.Fatalf("seconds: got %q", got)
	}
	if got := seconds(-40); got != "0.000000" {
		t.Fatalf("seconds clamps: got %q", got)
	}
}

func TestPlanAssemblyCountsJoins(t *testing.T) {
	kept := []RangeMs{{Start: 0, End: 5000}, {Start: 8000, End: 13000}}
	cold := []RangeMs{{Start: 14000, End: 20000}}
	planned := planAssembly(kept, cold)
	if planned.graph == "" {
		t.Fatalf("plan graph is empty")
	}
	if !strings.Contains(planned.graph, "[asm]") {
		t.Fatalf("plan graph never reaches the assembly label")
	}
	if len(planned.boundaries) != 3 {
		t.Fatalf("plan boundaries: got %v", planned.boundaries)
	}
	last := planned.boundaries[len(planned.boundaries)-1]
	if last <= 0 || last >= 5.75+0.75+10 {
		t.Fatalf("plan last boundary out of range: %v", last)
	}
}

func TestPlanAssemblyEmptyKeptPlansNothing(t *testing.T) {
	if planned := planAssembly(nil, []RangeMs{{Start: 0, End: 100}}); planned.graph != "" {
		t.Fatalf("empty kept should plan nothing: %q", planned.graph)
	}
}

func TestPlanAssemblyNoColdOpenStartsAtZero(t *testing.T) {
	kept := []RangeMs{{Start: 0, End: 3000}, {Start: 5000, End: 9000}}
	planned := planAssembly(kept, nil)
	if len(planned.boundaries) != 1 {
		t.Fatalf("no cold boundaries: got %v", planned.boundaries)
	}
	if planned.boundaries[0] != 3.0 {
		t.Fatalf("no cold boundary: got %v want 3.0", planned.boundaries[0])
	}
}
