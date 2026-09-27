package render

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"

	"github.com/nrynss/keel/ffmpeg"
)

func runTool(t *testing.T, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s %v: %v: %s", name, args, err, stderr.String())
	}
	return stderr.String()
}

// writeRoomStem synthesizes a user stem carrying gated tone bursts over
// steady white noise. The bursts stand in for speech with pauses, and
// the noise floor sits near a quiet room, so the mix can prove it drops
// the floor while leaving the bursts alone.
func writeRoomStem(t *testing.T, path string) {
	t.Helper()
	runTool(t, "ffmpeg", "-hide_banner", "-y",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=12:sample_rate=48000",
		"-f", "lavfi", "-i", "anoisesrc=color=white:duration=12:sample_rate=48000:amplitude=0.004:seed=7",
		"-filter_complex", "[0:a]volume=0.08,volume='if(lt(mod(t,1),0.4),1,0)':eval=frame[s];[1:a]aresample=48000[n];[s][n]amix=inputs=2:normalize=0",
		"-c:a", "pcm_s16le", "-ar", "48000", "-ac", "1", path)
}

// writeSilenceStem synthesizes a silent host stem of the same length.
func writeSilenceStem(t *testing.T, path string) {
	t.Helper()
	runTool(t, "ffmpeg", "-hide_banner", "-y",
		"-f", "lavfi", "-i", "anullsrc=r=48000:cl=mono:d=12",
		"-c:a", "pcm_s16le", "-ar", "48000", "-ac", "1", path)
}

// writeUndenoisedMix writes the same mix without the noise reducer.
// Folding mono to stereo lowers each channel on its own, so the before
// file has to take that fold too. astats then reads two written files.
func writeUndenoisedMix(t *testing.T, userPath, hostPath, dst string) {
	t.Helper()
	graph := fmt.Sprintf(
		"[0:a]aformat=sample_fmts=fltp:channel_layouts=stereo,aresample=%d,adelay=0|0[u];"+
			"[1:a]aformat=sample_fmts=fltp:channel_layouts=stereo,aresample=%d,adelay=0|0[h];"+
			"[u][h]amix=inputs=2:normalize=0[mix]",
		MixRate, MixRate)
	runTool(t, "ffmpeg", "-hide_banner", "-y", "-i", userPath, "-i", hostPath,
		"-filter_complex", graph, "-map", "[mix]",
		"-c:a", "pcm_s16le", "-ar", strconv.Itoa(MixRate), "-ac", "2", dst)
}

// rmsLevelDB reads the RMS level in dB of path after an optional
// measurement filter. An empty filter measures the file as is.
func rmsLevelDB(t *testing.T, path, measure string) float64 {
	t.Helper()
	filter := "astats"
	if measure != "" {
		filter = measure + ",astats"
	}
	out := runTool(t, "ffmpeg", "-hide_banner", "-i", path,
		"-af", filter, "-f", "null", "-")
	m := regexp.MustCompile(`RMS level dB:\s*(\S+)`).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("astats reported no RMS level for %s", path)
	}
	if m[1] == "-inf" {
		return -100
	}
	v, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		t.Fatalf("parse RMS level %q: %v", m[1], err)
	}
	return v
}

// gapLevelDB reads the floor from a pause between bursts.
func gapLevelDB(t *testing.T, path string) float64 {
	t.Helper()
	return rmsLevelDB(t, path, "atrim=start=2.5:end=2.9,asetpts=PTS-STARTPTS")
}

// speechLevelDB reads the burst level through the speech band.
func speechLevelDB(t *testing.T, path string) float64 {
	t.Helper()
	return rmsLevelDB(t, path, "atrim=start=2.05:end=2.35,asetpts=PTS-STARTPTS,highpass=f=300,lowpass=f=3400")
}

// decodeMono48 decodes path to mono 48 kHz samples in -1..1.
func decodeMono48(t *testing.T, path string) []float64 {
	t.Helper()
	raw := filepath.Join(t.TempDir(), "mono.pcm")
	runTool(t, "ffmpeg", "-hide_banner", "-y", "-i", path,
		"-ar", "48000", "-ac", "1", "-c:a", "pcm_s16le", "-f", "s16le", raw)
	buf, err := os.ReadFile(raw)
	if err != nil {
		t.Fatalf("read decoded samples: %v", err)
	}
	out := make([]float64, len(buf)/2)
	for i := range out {
		out[i] = float64(int16(binary.LittleEndian.Uint16(buf[i*2:]))) / 32768
	}
	return out
}

// alignmentLag returns the sample lag where b matches a best over a one
// second window, stepping coarsely for speed. A lag of zero means b
// carries the same clock as a.
func alignmentLag(a, b []float64, start int) int {
	window := 48000
	best := math.MaxFloat64
	lag := 0
	for l := -100; l <= 1500; l++ {
		sum := 0.0
		for i := 0; i < window; i += 7 {
			j := start + i + l
			if j < 0 || j >= len(b) {
				continue
			}
			d := a[start+i] - b[j]
			sum += d * d
		}
		if sum < best {
			best = sum
			lag = l
		}
	}
	return lag
}

func shaFile(t *testing.T, path string) [32]byte {
	t.Helper()
	buf, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("hash %s: %v", path, err)
	}
	return sha256.Sum256(buf)
}

func TestMixStemsLowersRoomFloorKeepsBursts(t *testing.T) {
	dir := t.TempDir()
	userPath := filepath.Join(dir, "user.wav")
	hostPath := filepath.Join(dir, "host.wav")
	plainPath := filepath.Join(dir, "plain.wav")
	mixPath := filepath.Join(dir, "mix.wav")
	writeRoomStem(t, userPath)
	writeSilenceStem(t, hostPath)
	writeUndenoisedMix(t, userPath, hostPath, plainPath)

	beforeGap := gapLevelDB(t, plainPath)
	beforeSpeech := speechLevelDB(t, plainPath)

	if err := mixStems(t.Context(), ffmpeg.Tools{}, userPath, 0, hostPath, 0, mixPath); err != nil {
		t.Fatalf("mix: %v", err)
	}

	afterGap := gapLevelDB(t, mixPath)
	drop := beforeGap - afterGap
	t.Logf("floor %.2f dB to %.2f dB, drop %.2f dB", beforeGap, afterGap, drop)
	if drop < 15 {
		t.Fatalf("floor drops only %.2f dB, want at least 15 (%.2f to %.2f)", drop, beforeGap, afterGap)
	}
	afterSpeech := speechLevelDB(t, mixPath)
	move := math.Abs(afterSpeech - beforeSpeech)
	t.Logf("speech band %.2f dB to %.2f dB, move %.2f dB", beforeSpeech, afterSpeech, move)
	if move >= 1 {
		t.Fatalf("speech band moves %.2f dB, want less than 1 (%.2f to %.2f)", move, beforeSpeech, afterSpeech)
	}
}

func TestMixStemsKeepsUserClock(t *testing.T) {
	dir := t.TempDir()
	userPath := filepath.Join(dir, "user.wav")
	hostPath := filepath.Join(dir, "host.wav")
	mixPath := filepath.Join(dir, "mix.wav")
	writeRoomStem(t, userPath)
	writeSilenceStem(t, hostPath)

	if err := mixStems(t.Context(), ffmpeg.Tools{}, userPath, 0, hostPath, 0, mixPath); err != nil {
		t.Fatalf("mix: %v", err)
	}
	user := decodeMono48(t, userPath)
	mixed := decodeMono48(t, mixPath)
	lag := alignmentLag(user, mixed, 2*48000)
	if lag < -240 || lag > 240 {
		t.Fatalf("mix lags the user stem by %d samples, want within 240", lag)
	}
}

func TestMixStemsLeavesStemBytesAlone(t *testing.T) {
	dir := t.TempDir()
	userPath := filepath.Join(dir, "user.wav")
	hostPath := filepath.Join(dir, "host.wav")
	mixPath := filepath.Join(dir, "mix.wav")
	writeRoomStem(t, userPath)
	writeSilenceStem(t, hostPath)
	beforeUser := shaFile(t, userPath)
	beforeHost := shaFile(t, hostPath)

	if err := mixStems(t.Context(), ffmpeg.Tools{}, userPath, 0, hostPath, 0, mixPath); err != nil {
		t.Fatalf("mix: %v", err)
	}
	if after := shaFile(t, userPath); after != beforeUser {
		t.Fatalf("mix rewrote the user stem")
	}
	if after := shaFile(t, hostPath); after != beforeHost {
		t.Fatalf("mix rewrote the host stem")
	}
}

func TestHashInputsVariesWithDenoise(t *testing.T) {
	base := Input{
		UserSHA256:    [32]byte{1},
		HostSHA256:    [32]byte{2},
		UserOffsetMs:  120,
		HostOffsetMs:  340,
		DurationMs:    1000,
		UserDenoiseNR: DenoiseNR,
	}
	changed := base
	changed.UserDenoiseNR = 0
	if HashInputs(base) == HashInputs(changed) {
		t.Fatalf("hash ignores the denoise tuning")
	}
}
