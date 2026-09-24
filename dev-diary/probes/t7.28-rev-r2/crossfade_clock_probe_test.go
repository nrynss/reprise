package render

import (
	"encoding/binary"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestProbeRenderClockAfterTwoCuts is a review probe. It runs the real
// assembly graph over a five second signal that is silent except for a
// burst at 4.5 s, with the one second spans at 1 s and 3 s cut. It then
// reads where the burst lands in the output. The episode page places
// that moment at 2.5 s. The file carries it at 2.48 s, because each of
// the two joins overlaps by a ten millisecond crossfade.
func TestProbeRenderClockAfterTwoCuts(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg is not on PATH")
	}
	kept := KeepRanges(5000, []RangeMs{{Start: 1000, End: 2000}, {Start: 3000, End: 4000}})
	plan := planAssembly(kept, nil)
	out := filepath.Join(t.TempDir(), "out.f32")
	cmd := exec.Command("ffmpeg", "-v", "error", "-y",
		"-f", "lavfi", "-i", "aevalsrc=if(between(t\\,4.5\\,4.505)\\,0.9\\,0):s=48000:c=stereo:d=5",
		"-filter_complex", plan.graph, "-map", "[asm]",
		"-f", "f32le", "-ac", "1", "-ar", "48000", out)
	if msg, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v: %s", err, msg)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	first := -1
	for i := 0; i+4 <= len(raw); i += 4 {
		v := math.Float32frombits(binary.LittleEndian.Uint32(raw[i:]))
		if v > 0.1 {
			first = i / 4
			break
		}
	}
	if first < 0 {
		t.Fatal("no burst in the output")
	}
	at := float64(first) / 48000
	t.Logf("burst lands at %.4f s, output length %.4f s", at, float64(len(raw)/4)/48000)
	if math.Abs(at-2.48) > 0.001 {
		t.Fatalf("burst at %.4f s, want 2.48 s", at)
	}
}
