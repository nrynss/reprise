package render

import (
	"encoding/binary"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestProbeColdOpenAcrossACut is a review probe. A six second take holds
// one second words, with bursts at 0.5 s, 2.5 s and 4.5 s. The second
// word is cut, and the cold open covers the first three words, so the
// opening itself carries a join. It runs the real assembly graph and
// reads each burst onset in the output. The episode page places the
// words of this draft at 2.72 s, 3.71 s and 5.71 s, so the episode
// bursts must land at 3.22 s, 4.21 s and 6.21 s.
func TestProbeColdOpenAcrossACut(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg is not on PATH")
	}
	kept := KeepRanges(6000, []RangeMs{{Start: 1000, End: 2000}})
	cold := IntersectRanges(RangeMs{Start: 0, End: 3000}, kept)
	plan := planAssembly(kept, cold)
	out := filepath.Join(t.TempDir(), "out.f32")
	src := "aevalsrc=if(between(t\\,0.5\\,0.505)+between(t\\,2.5\\,2.505)+between(t\\,4.5\\,4.505)\\,0.9\\,0):s=48000:c=stereo:d=6"
	cmd := exec.Command("ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", src,
		"-filter_complex", plan.graph, "-map", "[asm]",
		"-f", "f32le", "-ac", "1", "-ar", "48000", out)
	if msg, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v: %s", err, msg)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	var onsets []float64
	last := -1.0
	for i := 0; i+4 <= len(raw); i += 4 {
		v := math.Float32frombits(binary.LittleEndian.Uint32(raw[i:]))
		at := float64(i/4) / 48000
		if v > 0.1 && at-last > 0.05 {
			onsets = append(onsets, at)
		}
		if v > 0.1 {
			last = at
		}
	}
	t.Logf("onsets %v, output length %.4f s", onsets, float64(len(raw)/4)/48000)
	want := []float64{0.5, 1.49, 3.22, 4.21, 6.21}
	if len(onsets) != len(want) {
		t.Fatalf("onsets = %v, want %v", onsets, want)
	}
	for i := range want {
		if math.Abs(onsets[i]-want[i]) > 0.001 {
			t.Fatalf("onset %d at %.4f s, want %.2f s", i, onsets[i], want[i])
		}
	}
}
