// Recorded sessions arrive in the shipped layout: one directory per
// session holding the guest clips as user-*.wav files, the provider
// stereo mix as recording.ogg with the guest on the left channel, the
// socket event log as events.json, and the recording metadata as
// metadata.json. The OGG container decodes through the ffmpeg binary the
// gate pins, so raw OGG bytes still fail DecodeWAV by construction: they
// are not WAV. A playback-start record named clips.json maps each guest
// clip to its start in seconds on the local clock. Detector receipt times
// in the event log mark when the provider noticed speech, not when a clip
// started, so the driver refuses them as starts and fails loudly until the
// record lands.
package align

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// offsetsFile names the recorder clock starts for one fixture session in
// seconds on the shared capture clock.
const offsetsFile = "offsets.json"

// sessionOffsets carries the recorder starts one fixture session stored.
type sessionOffsets struct {
	UserStartSec float64 `json:"user_start_sec"`
	HostStartSec float64 `json:"host_start_sec"`
}

// measureSession reads one session directory and returns its report. It
// wants user.wav and host.wav beside the provider recording, a stereo
// recording with the user on the left channel and the host on the right,
// plus the recorder starts. A missing or unreadable wanted file is an
// error, never a skip, so a misnamed fixture file fails loudly instead
// of passing silently. The caller applies the drift verdict to the
// returned report.
func measureSession(dir string) (Report, error) {
	read := func(name string) ([]byte, error) {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("session %s has no %s: %w", dir, name, err)
		}
		return data, nil
	}
	userBytes, err := read("user.wav")
	if err != nil {
		return Report{}, err
	}
	hostBytes, err := read("host.wav")
	if err != nil {
		return Report{}, err
	}
	provider, err := loadProviderRecording(dir)
	if err != nil {
		return Report{}, err
	}
	userRec, err := DecodeWAV(userBytes)
	if err != nil {
		return Report{}, fmt.Errorf("session %s user.wav: %w", dir, err)
	}
	hostRec, err := DecodeWAV(hostBytes)
	if err != nil {
		return Report{}, fmt.Errorf("session %s host.wav: %w", dir, err)
	}
	if len(provider.Channels) != 2 {
		return Report{}, fmt.Errorf(
			"session %s provider has %d channels, want stereo",
			dir, len(provider.Channels),
		)
	}
	raw, err := read(offsetsFile)
	if err != nil {
		return Report{}, err
	}
	var offsets sessionOffsets
	if err := json.Unmarshal(raw, &offsets); err != nil {
		return Report{}, fmt.Errorf("session %s offsets: %w", dir, err)
	}
	measure := func(stem Recording, channel []float64) (Lag, error) {
		resampled, err := ResampleLinear(stem.Channels[0], stem.SampleRate, provider.SampleRate)
		if err != nil {
			return Lag{}, fmt.Errorf("session %s resample: %w", dir, err)
		}
		n := len(resampled)
		if len(channel) < n {
			n = len(channel)
		}
		width, err := SearchSamples(provider.SampleRate, 0.5)
		if err != nil {
			return Lag{}, fmt.Errorf("session %s search width: %w", dir, err)
		}
		lag, err := Correlate(channel[:n], resampled[:n], provider.SampleRate, width)
		if err != nil {
			return Lag{}, fmt.Errorf("session %s correlate: %w", dir, err)
		}
		return lag, nil
	}
	userLag, err := measure(userRec, provider.Channels[0])
	if err != nil {
		return Report{}, err
	}
	hostLag, err := measure(hostRec, provider.Channels[1])
	if err != nil {
		return Report{}, err
	}
	return Align(ClockOffsets(offsets), userLag, hostLag), nil
}

// TestFixtureSessions runs the alignment proof against every recorded
// session under testdata/sessions in the shipped layout. Each session
// must decode and locate: the driver reads the real files and never
// skips an existing directory. The verdict measures through the
// playback-start record and fails the episode past 40 ms of drift. A
// missing record fails loudly naming it. The ran count pins the
// criterion shape: exactly three sessions must measure.
func TestFixtureSessions(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "sessions")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Skipf("no recorded sessions at %s: %v", root, err)
	}
	measured := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		measured++
		t.Run(entry.Name(), func(t *testing.T) {
			dir := filepath.Join(root, entry.Name())
			locs, rate, err := locateSessionClips(dir)
			if err != nil {
				t.Fatalf("session %s: %v", dir, err)
			}
			if rate != providerRate {
				t.Fatalf("session %s rate = %d, want %d", dir, rate, providerRate)
			}
			if len(locs) != 2 {
				t.Fatalf("session %s located %d clips, want 2", dir, len(locs))
			}
			for _, loc := range locs {
				if loc.peak < 0.5 {
					t.Fatalf("session %s clip %s peak = %.2f, want at least 0.50", dir, loc.file, loc.peak)
				}
				if loc.peak-loc.runnerUp < 0.3 {
					t.Fatalf("session %s clip %s peak %.2f stands %.2f over runner-up %.2f, want 0.30 clear",
						dir, loc.file, loc.peak, loc.peak-loc.runnerUp, loc.runnerUp)
				}
				t.Logf("session %s clip %s onset %.4fs peak %.3f runner-up %.3f",
					dir, loc.file, loc.onsetSec, loc.peak, loc.runnerUp)
			}
			logVADGap(t, dir, locs)
			report, err := measureRealSession(dir)
			if err != nil {
				t.Fatalf("session %s: %v", dir, err)
			}
			t.Logf("session %s drift %.1fms", dir, report.DriftSec*1000)
			if !report.Pass() {
				t.Fatalf("session %s drifted by %.1f ms: %s", dir, report.DriftSec*1000, report.Warning())
			}
		})
	}
	if measured != 3 {
		t.Fatalf("measured %d sessions under %s, want 3", measured, root)
	}
}

// logVADGap records the diagnostic gap between the detector receipt times
// in the event log and the correlation onsets. Detector times are not
// start records, so this log informs but never verdicts.
func logVADGap(t *testing.T, dir string, locs []locatedClip) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "events.json"))
	if err != nil {
		t.Logf("session %s has no event log: %v", dir, err)
		return
	}
	var events []struct {
		Offset string `json:"offset"`
		Type   string `json:"type"`
	}
	if err := json.Unmarshal(raw, &events); err != nil {
		t.Logf("session %s event log: %v", dir, err)
		return
	}
	var starts []float64
	for _, e := range events {
		if e.Type != "input.speech.started" {
			continue
		}
		d, err := time.ParseDuration(e.Offset)
		if err != nil {
			t.Logf("session %s bad offset %q: %v", dir, e.Offset, err)
			return
		}
		starts = append(starts, d.Seconds())
	}
	if len(starts) != len(locs) {
		t.Logf("session %s holds %d detector starts for %d clips", dir, len(starts), len(locs))
		return
	}
	sort.Float64s(starts)
	onsets := []float64{locs[0].onsetSec, locs[1].onsetSec}
	sort.Float64s(onsets)
	t.Logf("session %s detector gap %.4fs measured gap %.4fs drift %.1fms (diagnostic only)",
		dir, starts[1]-starts[0], onsets[1]-onsets[0],
		((onsets[1]-onsets[0])-(starts[1]-starts[0]))*1000)
}

// writeDriverSession writes one synthetic session layout to dir. The stems
// hold the given signals and the provider stereo carries their delayed
// copies with the user on the left and the host on the right.
func writeDriverSession(
	t *testing.T,
	dir string,
	user, host, providerLeft, providerRight []float64,
	rate int,
	clock ClockOffsets,
) {
	t.Helper()
	write := func(name string, channels [][]float64) {
		t.Helper()
		data, err := EncodeWAV(channels, rate)
		if err != nil {
			t.Fatalf("EncodeWAV %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	write("user.wav", [][]float64{user})
	write("host.wav", [][]float64{host})
	write("provider.wav", [][]float64{providerLeft, providerRight})
	raw, err := json.Marshal(sessionOffsets(clock))
	if err != nil {
		t.Fatalf("marshal offsets: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, offsetsFile), raw, 0o600); err != nil {
		t.Fatalf("write %s: %v", offsetsFile, err)
	}
}

// TestDriverMeasuresSyntheticSessions proves the file driver measures
// instead of skipping. One layout plays both stems through a common path
// latency and must pass. A second layout delays the host provider channel
// by 400 extra samples and must fail the check with exactly 50 ms of
// drift. The ran count pins that both layouts measured.
func TestDriverMeasuresSyntheticSessions(t *testing.T) {
	const rate = 8000
	user := chirp(6000, 300, 1500, rate)
	host := chirp(6000, 1500, 300, rate)
	const pathLatency = 150
	const extraHostDelay = 400
	clock := ClockOffsets{UserStartSec: 0.25, HostStartSec: 1.0}

	measured := 0

	t.Run("aligned", func(t *testing.T) {
		dir := t.TempDir()
		writeDriverSession(t, dir,
			user, host,
			delayed(user, pathLatency), delayed(host, pathLatency),
			rate, clock)
		report, err := measureSession(dir)
		if err != nil {
			t.Fatalf("measureSession returned %v", err)
		}
		if !report.Pass() {
			t.Fatalf("aligned session drifted by %.1f ms", report.DriftSec*1000)
		}
		if report.DriftSec != 0 {
			t.Fatalf("drift = %v, want 0", report.DriftSec)
		}
		measured++
	})

	t.Run("drifted", func(t *testing.T) {
		dir := t.TempDir()
		writeDriverSession(t, dir,
			user, host,
			delayed(user, pathLatency), delayed(host, pathLatency+extraHostDelay),
			rate, clock)
		report, err := measureSession(dir)
		if err != nil {
			t.Fatalf("measureSession returned %v", err)
		}
		if report.Pass() {
			t.Fatalf("drifted session passed the check")
		}
		want := float64(-extraHostDelay)
		if got := math.Round(report.DriftSec * float64(rate)); got != want {
			t.Fatalf("drift = %v samples, want %v", got, want)
		}
		if report.Warning() == "" {
			t.Fatalf("failed check gave no warning")
		}
		measured++
	})

	if measured != 2 {
		t.Fatalf("measured %d synthetic sessions, want 2", measured)
	}
}

// TestDriverRejectsStoredBytes pins both sides of the container line.
// Raw OGG bytes are not WAV, so DecodeWAV rejects the OGG magic with
// ErrWAV, and a session whose WAV file holds stored bytes errors instead
// of measuring. A real OGG container takes the other path: the driver
// decodes it through the pinned ffmpeg binary and measures through it.
func TestDriverRejectsStoredBytes(t *testing.T) {
	ogg := make([]byte, 48)
	copy(ogg, "OggS")
	if _, err := DecodeWAV(ogg); !errors.Is(err, ErrWAV) {
		t.Fatalf("ogg magic err = %v, want ErrWAV", err)
	}
	const rate = 8000
	user := chirp(6000, 300, 1500, rate)
	host := chirp(6000, 1500, 300, rate)
	const pathLatency = 150
	dir := t.TempDir()
	writeDriverSession(t, dir,
		user, host,
		delayed(user, pathLatency), delayed(host, pathLatency),
		rate, ClockOffsets{UserStartSec: 0.25, HostStartSec: 1.0})
	if err := os.WriteFile(filepath.Join(dir, "provider.wav"), ogg, 0o600); err != nil {
		t.Fatalf("write stored bytes: %v", err)
	}
	if _, err := measureSession(dir); !errors.Is(err, ErrWAV) {
		t.Fatalf("stored bytes err = %v, want ErrWAV", err)
	}

	// A genuine OGG provider recording decodes instead of rejecting.
	oggDir := t.TempDir()
	writeDriverSession(t, oggDir,
		user, host,
		delayed(user, pathLatency), delayed(host, pathLatency),
		rate, ClockOffsets{UserStartSec: 0.25, HostStartSec: 1.0})
	wavBytes, err := os.ReadFile(filepath.Join(oggDir, "provider.wav"))
	if err != nil {
		t.Fatalf("read provider.wav: %v", err)
	}
	oggBytes := encodeProviderOGG(t, wavBytes)
	if err := os.Remove(filepath.Join(oggDir, "provider.wav")); err != nil {
		t.Fatalf("remove provider.wav: %v", err)
	}
	if err := os.WriteFile(filepath.Join(oggDir, "provider.ogg"), oggBytes, 0o600); err != nil {
		t.Fatalf("write provider.ogg: %v", err)
	}
	report, err := measureSession(oggDir)
	if err != nil {
		t.Fatalf("measureSession over provider.ogg returned %v", err)
	}
	if !report.Pass() {
		t.Fatalf("ogg session drifted by %.1f ms", report.DriftSec*1000)
	}
	if report.DriftSec != 0 {
		t.Fatalf("ogg drift = %v, want 0", report.DriftSec)
	}
}

// TestDriverFailsLoudOnWrongLayout pins the rename mutation. A session
// directory with a misnamed stem file is an error, never a skip.
func TestDriverFailsLoudOnWrongLayout(t *testing.T) {
	const rate = 8000
	user := chirp(6000, 300, 1500, rate)
	host := chirp(6000, 1500, 300, rate)
	const pathLatency = 150
	dir := t.TempDir()
	writeDriverSession(t, dir,
		user, host,
		delayed(user, pathLatency), delayed(host, pathLatency),
		rate, ClockOffsets{UserStartSec: 0.25, HostStartSec: 1.0})
	if err := os.Rename(
		filepath.Join(dir, "user.wav"),
		filepath.Join(dir, "user_stem.wav"),
	); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if _, err := measureSession(dir); err == nil {
		t.Fatalf("misnamed stem measured, want a layout error")
	}
}

// providerRate is the session stream rate in hertz. The socket input and
// output formats in the event log both declare 24000 Hz, so the provider
// recording decodes at that rate and a clip streamed on the session clock
// occupies one provider sample per file sample.
const providerRate = 24000

// clipStartsFile names the playback-start record. It maps each guest clip
// file to its start in seconds on the local clock, the same clock the
// event log offsets use.
const clipStartsFile = "clips.json"

// clipStart maps one streamed guest clip to its start on the local clock.
type clipStart struct {
	// File is the clip base name, such as user-a.wav.
	File string `json:"file"`
	// StartSec is the local clock reading at the first clip sample.
	StartSec float64 `json:"start_sec"`
}

// sessionMetadata carries the recording integrity facts the driver
// checks before measuring.
type sessionMetadata struct {
	// Channels is the provider recording channel count.
	Channels int `json:"channels"`
	// DroppedChunks counts provider recording chunks lost in transit.
	DroppedChunks int `json:"dropped_chunks"`
}

// locatedClip places one guest clip inside the provider guest channel.
type locatedClip struct {
	// file is the clip base name.
	file string
	// onsetSec counts from the provider recording start to the first
	// clip sample.
	onsetSec float64
	// peak is the normalized correlation at the onset.
	peak float64
	// runnerUp is the best normalized correlation outside the
	// winning footprint.
	runnerUp float64
}

// loadProviderRecording decodes the provider mix for the synthetic
// layout. A WAV file keeps the old contract working byte for byte: raw
// container bytes still fail the WAV magic. An OGG file decodes through
// the pinned ffmpeg binary. Anything else is a layout error.
func loadProviderRecording(dir string) (Recording, error) {
	if data, err := os.ReadFile(filepath.Join(dir, "provider.wav")); err == nil {
		provider, derr := DecodeWAV(data)
		if derr != nil {
			return Recording{}, fmt.Errorf("session %s provider.wav: %w", dir, derr)
		}
		if len(provider.Channels) != 2 {
			return Recording{}, fmt.Errorf(
				"session %s provider has %d channels, want stereo",
				dir, len(provider.Channels),
			)
		}
		return provider, nil
	}
	ogg, err := os.ReadFile(filepath.Join(dir, "provider.ogg"))
	if err != nil {
		return Recording{}, fmt.Errorf("session %s has no provider recording: %w", dir, err)
	}
	provider, err := decodeOGGBytes(ogg, filepath.Join(dir, "provider.ogg"))
	if err != nil {
		return Recording{}, err
	}
	if len(provider.Channels) != 2 {
		return Recording{}, fmt.Errorf(
			"session %s provider has %d channels, want stereo",
			dir, len(provider.Channels),
		)
	}
	return provider, nil
}

// decodeOGGBytes decodes one OGG/Opus provider recording to stereo PCM
// at the session stream rate. It shells out to the ffmpeg binary the
// gate pins instead of growing a codec dependency: the measuring tool
// runs where the check runs, and the gate fails by name when it is
// absent. The label names the file under test for loud errors.
func decodeOGGBytes(data []byte, label string) (Recording, error) {
	cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error",
		"-i", "pipe:0", "-ar", "24000", "-ac", "2",
		"-f", "wav", "-acodec", "pcm_s16le", "pipe:1")
	cmd.Stdin = bytes.NewReader(data)
	var out bytes.Buffer
	var errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return Recording{}, fmt.Errorf("decode %s through ffmpeg: %v: %s",
			label, err, strings.TrimSpace(errBuf.String()))
	}
	rec, err := DecodeWAV(out.Bytes())
	if err != nil {
		return Recording{}, fmt.Errorf("decode %s ffmpeg output: %w", label, err)
	}
	return rec, nil
}

// encodeProviderOGG encodes WAV bytes to an OGG/Opus container through
// the pinned ffmpeg binary. Tests use it to build genuine provider
// recordings the driver must decode rather than reject.
func encodeProviderOGG(t *testing.T, wavBytes []byte) []byte {
	t.Helper()
	cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error",
		"-i", "pipe:0", "-acodec", "libopus", "-f", "ogg", "pipe:1")
	cmd.Stdin = bytes.NewReader(wavBytes)
	var out bytes.Buffer
	var errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		t.Fatalf("encode provider.ogg through ffmpeg: %v: %s", err, strings.TrimSpace(errBuf.String()))
	}
	return out.Bytes()
}

// coarsePrep reduces a signal to its energy contour at decimated
// resolution. Onset finding matches contours first: speech and
// syllable-shaped tones carry distinctive amplitude shapes while their
// carriers alias into mush at these factors. The fine walk below still
// scores raw waveforms, so precision never depends on the contour.
func coarsePrep(x []float64, factor int) []float64 {
	if factor <= 1 {
		out := make([]float64, len(x))
		copy(out, x)
		return out
	}
	out := make([]float64, len(x)/factor)
	for i := range out {
		var sum float64
		for j := 0; j < factor; j++ {
			v := x[i*factor+j]
			if v < 0 {
				v = -v
			}
			sum += v
		}
		out[i] = sum / float64(factor)
	}
	return out
}

// scoreLag returns the normalized correlation of clip against ref at one
// lag, with silence reporting no score instead of a number.
func scoreLag(ref, clip []float64, lag int, clipEnergy float64) (float64, bool) {
	var dot, refEnergy float64
	for i, cv := range clip {
		rv := ref[lag+i]
		dot += rv * cv
		refEnergy += rv * rv
	}
	if refEnergy == 0 {
		return 0, false
	}
	return dot / math.Sqrt(refEnergy*clipEnergy), true
}

// coarseScan finds the best lag of clip inside ref at decimated
// resolution. It returns the best lag with its score and the best score
// outside exclude coarse bins around it, so a wide true peak never masks
// itself as its own runner-up. Scores center each window first: contours
// are non-negative, and an uncentered score would read their shared
// baseline as agreement everywhere.
func coarseScan(ref, clip []float64, factor, exclude int) (bestLag int, best, second float64) {
	r := coarsePrep(ref, factor)
	c := coarsePrep(clip, factor)
	var clipMean float64
	for _, v := range c {
		clipMean += v
	}
	clipMean /= float64(len(c))
	cc := make([]float64, len(c))
	var clipEnergy float64
	for i, v := range c {
		cc[i] = v - clipMean
		clipEnergy += cc[i] * cc[i]
	}
	score := func(lag int) (float64, bool) {
		var refMean float64
		for i := range cc {
			refMean += r[lag+i]
		}
		refMean /= float64(len(cc))
		var dot, refEnergy float64
		for i, cv := range cc {
			rv := r[lag+i] - refMean
			dot += rv * cv
			refEnergy += rv * rv
		}
		if refEnergy == 0 || clipEnergy == 0 {
			return 0, false
		}
		return dot / math.Sqrt(refEnergy*clipEnergy), true
	}
	best = math.Inf(-1)
	bestLag = -1
	for lag := 0; lag+len(cc) <= len(r); lag++ {
		s, ok := score(lag)
		if !ok {
			continue
		}
		if s > best {
			best = s
			bestLag = lag
		}
	}
	second = math.Inf(-1)
	for lag := 0; lag+len(cc) <= len(r); lag++ {
		if bestLag >= 0 && lag >= bestLag-exclude && lag <= bestLag+exclude {
			continue
		}
		s, ok := score(lag)
		if !ok {
			continue
		}
		if s > second {
			second = s
		}
	}
	return bestLag, best, second
}

// refineLag walks half samples either side of center at full resolution
// and returns the best lag with its normalized score.
func refineLag(ref, clip []float64, center, half int) (int, float64) {
	clipEnergy := 0.0
	for _, v := range clip {
		clipEnergy += v * v
	}
	lo := center - half
	if lo < 0 {
		lo = 0
	}
	hi := center + half
	if hi > len(ref)-len(clip) {
		hi = len(ref) - len(clip)
	}
	bestLag, best := lo, math.Inf(-1)
	for lag := lo; lag <= hi; lag++ {
		s, ok := scoreLag(ref, clip, lag, clipEnergy)
		if !ok {
			continue
		}
		if s > best {
			best = s
			bestLag = lag
		}
	}
	return bestLag, best
}

// clipCandidates holds the two placements a streamed clip may have. The
// file samples may run on the session clock, occupying one provider
// sample each, or decoded on the file clock, needing a resample to the
// provider rate. Committed fixtures measure direct, but the locator
// tries both and keeps the stronger peak instead of assuming one.
func clipCandidates(clip []float64, clipRate, rate int) ([][]float64, error) {
	cands := [][]float64{clip}
	if clipRate != rate {
		rs, err := ResampleLinear(clip, clipRate, rate)
		if err != nil {
			return nil, err
		}
		cands = append(cands, rs)
	}
	return cands, nil
}

// locateClip finds where clip sits inside the provider guest channel and
// returns the onset in seconds with the peak and runner-up scores. A
// coarse contour scan picks the winning placement hypothesis, then a
// full-resolution walk on raw waveforms fixes the exact onset. Peaks
// below 0.50 are not matches, and a winner must stand 0.30 clear of the
// field: both fail loudly instead of reporting a guess.
func locateClip(channel []float64, rate int, clip []float64, clipRate int) (onsetSec, peak, runnerUp float64, err error) {
	if rate <= 0 || clipRate <= 0 {
		return 0, 0, 0, fmt.Errorf("align: rates %d and %d: %w", rate, clipRate, ErrSampleRate)
	}
	if len(clip) == 0 || len(channel) == 0 {
		return 0, 0, 0, fmt.Errorf("align: empty signal: %w", ErrSignal)
	}
	cands, err := clipCandidates(clip, clipRate, rate)
	if err != nil {
		return 0, 0, 0, err
	}
	factor := rate / 500
	if factor < 1 {
		factor = 1
	}
	found := false
	winCand, winLag := 0, 0
	winBest, winSecond := math.Inf(-1), math.Inf(-1)
	for ci, cand := range cands {
		if len(channel) < len(cand) {
			continue
		}
		// The runner-up mask spans the whole clip: a lag that still
		// overlaps the winning footprint is the same placement seen
		// through silence, not a competing one.
		exclude := (len(cand) + factor - 1) / factor
		lag, best, second := coarseScan(channel, cand, factor, exclude)
		if lag < 0 {
			continue
		}
		if !found || best > winBest {
			found = true
			winCand, winLag, winBest, winSecond = ci, lag, best, second
		}
	}
	if !found {
		return 0, 0, 0, fmt.Errorf("align: clip longer than the provider channel: %w", ErrSignal)
	}
	lag, score := refineLag(channel, cands[winCand], winLag*factor, rate/50)
	if score < 0.5 {
		return 0, 0, 0, fmt.Errorf("align: no confident match, peak %.2f, want at least 0.50: %w", score, ErrSignal)
	}
	if score-winSecond < 0.3 {
		return 0, 0, 0, fmt.Errorf("align: ambiguous match, peak %.2f stands %.2f over runner-up %.2f, want 0.30 clear: %w",
			score, score-winSecond, winSecond, ErrSignal)
	}
	return float64(lag) / float64(rate), score, winSecond, nil
}

// hostPeak scores the guest clip against the host channel at full
// resolution around the contour-best lag of each placement hypothesis.
// Speech contours correlate moderately across channels even when the
// waveforms share nothing, so the contour alone cannot prove the guest
// stays off the host channel. The waveform peak can: committed fixtures
// measure 0.10 and below. A channel with no contour anywhere passes
// trivially, since the guest is absent by inspection.
func hostPeak(host []float64, rate int, clip []float64, clipRate int) float64 {
	cands, err := clipCandidates(clip, clipRate, rate)
	if err != nil {
		return math.Inf(-1)
	}
	factor := rate / 500
	if factor < 1 {
		factor = 1
	}
	best := math.Inf(-1)
	for _, cand := range cands {
		if len(host) < len(cand) {
			continue
		}
		lag, _, _ := coarseScan(host, cand, factor, 0)
		if lag < 0 {
			continue
		}
		if _, s := refineLag(host, cand, lag*factor, rate/50); s > best {
			best = s
		}
	}
	return best
}

// checkEventLog verifies the socket event log parses as a non-empty JSON
// array. The driver reads no starts from it: detector receipt times mark
// when the provider noticed speech, not when a clip started.
func checkEventLog(dir string) error {
	raw, err := os.ReadFile(filepath.Join(dir, "events.json"))
	if err != nil {
		return fmt.Errorf("session %s has no events.json: %w", dir, err)
	}
	var events []struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &events); err != nil {
		return fmt.Errorf("session %s events.json: %w", dir, err)
	}
	if len(events) == 0 {
		return fmt.Errorf("session %s events.json holds no events", dir)
	}
	return nil
}

// readSessionMetadata reads the recording integrity record. A dropped
// chunk means the provider mix is incomplete, so measurement refuses it
// instead of aligning against a gappy reference.
func readSessionMetadata(dir string) (sessionMetadata, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "metadata.json"))
	if err != nil {
		return sessionMetadata{}, fmt.Errorf("session %s has no metadata.json: %w", dir, err)
	}
	var meta sessionMetadata
	if err := json.Unmarshal(raw, &meta); err != nil {
		return sessionMetadata{}, fmt.Errorf("session %s metadata.json: %w", dir, err)
	}
	if meta.Channels != 2 {
		return sessionMetadata{}, fmt.Errorf("session %s metadata.json reports %d channels, want stereo", dir, meta.Channels)
	}
	if meta.DroppedChunks != 0 {
		return sessionMetadata{}, fmt.Errorf("session %s metadata.json reports %d dropped chunks", dir, meta.DroppedChunks)
	}
	return meta, nil
}

// readClipStarts reads the playback-start record. Its absence is a loud
// error naming the file and the shape it must carry, never a skip.
func readClipStarts(dir string) ([]clipStart, error) {
	raw, err := os.ReadFile(filepath.Join(dir, clipStartsFile))
	if err != nil {
		return nil, fmt.Errorf("session %s has no %s: %w. The record must map each user-*.wav file to its start in seconds on the local clock",
			dir, clipStartsFile, err)
	}
	var starts []clipStart
	if err := json.Unmarshal(raw, &starts); err != nil {
		return nil, fmt.Errorf("session %s %s: %w", dir, clipStartsFile, err)
	}
	if len(starts) < 2 {
		return nil, fmt.Errorf("session %s %s holds %d starts, want at least 2", dir, clipStartsFile, len(starts))
	}
	seen := map[string]bool{}
	for _, s := range starts {
		if s.File == "" || s.StartSec < 0 {
			return nil, fmt.Errorf("session %s %s holds a bad start %+v", dir, clipStartsFile, s)
		}
		if seen[s.File] {
			return nil, fmt.Errorf("session %s %s names %s twice", dir, clipStartsFile, s.File)
		}
		seen[s.File] = true
	}
	return starts, nil
}

// locateSessionClips decodes one recorded session in the shipped layout
// and locates every guest clip on the provider guest (left) channel. A
// missing or unreadable wanted file is an error naming the file, never
// a skip. A clip that also matches the host channel errors too: the
// guest sits on the left, so a host match means the layout changed.
func locateSessionClips(dir string) ([]locatedClip, int, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "user-*.wav"))
	if err != nil {
		return nil, 0, fmt.Errorf("session %s clip glob: %w", dir, err)
	}
	sort.Strings(matches)
	if len(matches) < 2 {
		return nil, 0, fmt.Errorf("session %s holds %d guest clips, want at least 2", dir, len(matches))
	}
	if err := checkEventLog(dir); err != nil {
		return nil, 0, err
	}
	if _, err := readSessionMetadata(dir); err != nil {
		return nil, 0, err
	}
	ogg, err := os.ReadFile(filepath.Join(dir, "recording.ogg"))
	if err != nil {
		return nil, 0, fmt.Errorf("session %s has no recording.ogg: %w", dir, err)
	}
	provider, err := decodeOGGBytes(ogg, filepath.Join(dir, "recording.ogg"))
	if err != nil {
		return nil, 0, err
	}
	if len(provider.Channels) != 2 {
		return nil, 0, fmt.Errorf("session %s provider has %d channels, want stereo", dir, len(provider.Channels))
	}
	rate := provider.SampleRate
	var locs []locatedClip
	for _, m := range matches {
		base := filepath.Base(m)
		raw, err := os.ReadFile(m)
		if err != nil {
			return nil, 0, fmt.Errorf("session %s clip %s: %w", dir, base, err)
		}
		clip, err := DecodeWAV(raw)
		if err != nil {
			return nil, 0, fmt.Errorf("session %s clip %s: %w", dir, base, err)
		}
		if len(clip.Channels) != 1 {
			return nil, 0, fmt.Errorf("session %s clip %s has %d channels, want mono", dir, base, len(clip.Channels))
		}
		onset, peak, second, err := locateClip(provider.Channels[0], rate, clip.Channels[0], clip.SampleRate)
		if err != nil {
			return nil, 0, fmt.Errorf("session %s clip %s: %w", dir, base, err)
		}
		if right := hostPeak(provider.Channels[1], rate, clip.Channels[0], clip.SampleRate); right > 0.30 {
			return nil, 0, fmt.Errorf("session %s clip %s matches the host channel at %.2f, want the guest on the left", dir, base, right)
		}
		locs = append(locs, locatedClip{file: base, onsetSec: onset, peak: peak, runnerUp: second})
	}
	return locs, rate, nil
}

// alignLocatedClips pairs each located clip with its playback start by
// file name and proves the relative placement. The two clips share one
// stream path, so the gap between their onsets cancels the path latency
// and must reproduce the gap between their starts. Exactly two clips
// keep the pair unambiguous.
func alignLocatedClips(locs []locatedClip, starts []clipStart, rate int) (Report, error) {
	if rate <= 0 {
		return Report{}, fmt.Errorf("align: rate %d: %w", rate, ErrSampleRate)
	}
	if len(locs) != 2 || len(starts) != 2 {
		return Report{}, fmt.Errorf("align: want exactly 2 clips and 2 starts, have %d and %d: %w",
			len(locs), len(starts), ErrSignal)
	}
	byName := map[string]float64{}
	for _, s := range starts {
		byName[s.File] = s.StartSec
	}
	type pair struct {
		onset float64
		start float64
	}
	pairs := make([]pair, 0, 2)
	for _, loc := range locs {
		start, ok := byName[loc.file]
		if !ok {
			return Report{}, fmt.Errorf("align: no start for clip %s: %w", loc.file, ErrSignal)
		}
		pairs = append(pairs, pair{onset: loc.onsetSec, start: start})
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].start < pairs[j].start })
	userLag := Lag{
		Samples: int(math.Round((pairs[0].onset - pairs[0].start) * float64(rate))),
		Seconds: pairs[0].onset - pairs[0].start,
	}
	hostLag := Lag{
		Samples: int(math.Round((pairs[1].onset - pairs[1].start) * float64(rate))),
		Seconds: pairs[1].onset - pairs[1].start,
	}
	clock := ClockOffsets{UserStartSec: pairs[0].start, HostStartSec: pairs[1].start}
	return Align(clock, userLag, hostLag), nil
}

// measureRealSession measures one recorded session in the shipped
// layout and returns its drift report. Layout and data problems fail
// loudly; a missing playback-start record fails naming the record.
func measureRealSession(dir string) (Report, error) {
	locs, rate, err := locateSessionClips(dir)
	if err != nil {
		return Report{}, err
	}
	starts, err := readClipStarts(dir)
	if err != nil {
		return Report{}, err
	}
	return alignLocatedClips(locs, starts, rate)
}

// placeClip embeds clip into a silent run of total samples at offset at.
func placeClip(total int, clip []float64, at int) []float64 {
	out := make([]float64, total)
	copy(out[at:], clip)
	return out
}

// writeRealLayoutSession writes one synthetic session in the shipped
// layout. The guest clips run at clipRate while the provider mix runs at
// provRate, mirroring streamed file samples on the session clock: the
// placed copies use the raw clip samples. The start record carries the
// matching gap, so an aligned layout passes with zero drift.
func writeRealLayoutSession(
	t *testing.T,
	dir string,
	clipA, clipB []float64,
	clipRate, provRate, placeA, placeB int,
	startA, startB float64,
) {
	t.Helper()
	write := func(name string, channels [][]float64, rate int) {
		t.Helper()
		data, err := EncodeWAV(channels, rate)
		if err != nil {
			t.Fatalf("EncodeWAV %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	write("user-a.wav", [][]float64{clipA}, clipRate)
	write("user-b.wav", [][]float64{clipB}, clipRate)
	total := placeB + len(clipB) + provRate
	left := placeClip(total, clipA, placeA)
	overlay := placeClip(total, clipB, placeB)
	for i := range left {
		left[i] += overlay[i]
	}
	host := chirp(total, 900, 400, float64(provRate))
	wavBytes, err := EncodeWAV([][]float64{left, host}, provRate)
	if err != nil {
		t.Fatalf("EncodeWAV provider: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "recording.ogg"), encodeProviderOGG(t, wavBytes), 0o600); err != nil {
		t.Fatalf("write recording.ogg: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "events.json"), []byte(`[{"type":"marker"}]`), 0o600); err != nil {
		t.Fatalf("write events.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "metadata.json"), []byte(`{"channels":2,"dropped_chunks":0}`), 0o600); err != nil {
		t.Fatalf("write metadata.json: %v", err)
	}
	starts, err := json.Marshal([]clipStart{{File: "user-a.wav", StartSec: startA}, {File: "user-b.wav", StartSec: startB}})
	if err != nil {
		t.Fatalf("marshal starts: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, clipStartsFile), starts, 0o600); err != nil {
		t.Fatalf("write %s: %v", clipStartsFile, err)
	}
}

// syllables builds a deterministic speech-like signal: consecutive
// 100ms blocks sweep between unrelated pitches at unrelated gains, so
// the contour never repeats itself the way a single swept tone does. A
// bare chirp correlates with shifted copies of itself closely enough to
// look ambiguous, which speech never does. The seed keeps clips apart:
// different seeds share no block shapes.
func syllables(total, rate int, seed uint64) []float64 {
	out := make([]float64, total)
	state := seed
	next := func() float64 {
		state = state*6364136223846793005 + 1442695040888963407
		return float64(state>>33) / float64(1<<31)
	}
	block := rate / 10
	phase := 0.0
	for b := 0; b*block < total; b++ {
		f0 := 200 + 700*next()
		f1 := 200 + 700*next()
		amp := 0.05 + 0.95*next()
		n := block
		if (b+1)*block > total {
			n = total - b*block
		}
		for i := 0; i < n; i++ {
			f := f0 + (f1-f0)*float64(i)/float64(n)
			phase += 2 * math.Pi * f / float64(rate)
			out[b*block+i] = math.Sin(phase) * amp
		}
	}
	return out
}

// TestLocateClipFindsBothPlacements proves the locator handles either
// way a clip may ride the session clock. One layout places the raw file
// samples, the other places the file-rate-correct resample. Both must
// locate within one sample with a strong peak.
func TestLocateClipFindsBothPlacements(t *testing.T) {
	const fileRate = 22050
	const provRate = 24000
	clip := syllables(44100, fileRate, 1)
	rs, err := ResampleLinear(clip, fileRate, provRate)
	if err != nil {
		t.Fatalf("ResampleLinear returned %v", err)
	}
	channel := make([]float64, provRate*4)
	const at = provRate
	copy(channel[at:], rs)
	onset, peak, _, err := locateClip(channel, provRate, clip, fileRate)
	if err != nil {
		t.Fatalf("locateClip resampled returned %v", err)
	}
	if got := math.Round(onset * provRate); got != at {
		t.Fatalf("resampled onset = %v samples, want %d", got, at)
	}
	if peak < 0.9 {
		t.Fatalf("resampled peak = %.2f, want at least 0.90", peak)
	}

	direct := make([]float64, provRate*4)
	copy(direct[at:], clip)
	onset, peak, _, err = locateClip(direct, provRate, clip, fileRate)
	if err != nil {
		t.Fatalf("locateClip direct returned %v", err)
	}
	if got := math.Round(onset * provRate); got != at {
		t.Fatalf("direct onset = %v samples, want %d", got, at)
	}
	if peak < 0.9 {
		t.Fatalf("direct peak = %.2f, want at least 0.90", peak)
	}
}

// TestRealSessionVerdictPath proves the shipped-layout verdict end to
// end on generated signals. One layout carries the matching start gap
// and must pass with zero drift. A second layout delays the second clip
// by 1200 samples and must fail the check with exactly that drift. A
// third layout drops the start record and must fail loudly naming it.
// The ran count pins that all three layouts measured.
func TestRealSessionVerdictPath(t *testing.T) {
	const clipRate = 22050
	const provRate = 24000
	clipA := syllables(44100, clipRate, 1)
	clipB := syllables(44100, clipRate, 2)
	const placeA = provRate
	const placeB = provRate * 4
	gapSec := float64(placeB-placeA) / float64(provRate)

	measured := 0

	t.Run("aligned", func(t *testing.T) {
		dir := t.TempDir()
		writeRealLayoutSession(t, dir, clipA, clipB, clipRate, provRate, placeA, placeB, 1.0, 1.0+gapSec)
		report, err := measureRealSession(dir)
		if err != nil {
			t.Fatalf("measureRealSession returned %v", err)
		}
		if !report.Pass() {
			t.Fatalf("aligned session drifted by %.1f ms", report.DriftSec*1000)
		}
		if report.DriftSec != 0 {
			t.Fatalf("drift = %v, want 0", report.DriftSec)
		}
		measured++
	})

	t.Run("drifted", func(t *testing.T) {
		const extra = 1200
		dir := t.TempDir()
		writeRealLayoutSession(t, dir, clipA, clipB, clipRate, provRate, placeA, placeB+extra, 1.0, 1.0+gapSec)
		report, err := measureRealSession(dir)
		if err != nil {
			t.Fatalf("measureRealSession returned %v", err)
		}
		if report.Pass() {
			t.Fatalf("drifted session passed the check")
		}
		if got := math.Round(report.DriftSec * float64(provRate)); got != extra {
			t.Fatalf("drift = %v samples, want %d", got, extra)
		}
		if report.Warning() == "" {
			t.Fatalf("failed check gave no warning")
		}
		measured++
	})

	t.Run("no-starts", func(t *testing.T) {
		dir := t.TempDir()
		writeRealLayoutSession(t, dir, clipA, clipB, clipRate, provRate, placeA, placeB, 1.0, 1.0+gapSec)
		if err := os.Remove(filepath.Join(dir, clipStartsFile)); err != nil {
			t.Fatalf("remove %s: %v", clipStartsFile, err)
		}
		if _, err := measureRealSession(dir); err == nil {
			t.Fatalf("missing start record measured, want a loud error")
		} else if !strings.Contains(err.Error(), clipStartsFile) {
			t.Fatalf("missing record err = %v, want it to name %s", err, clipStartsFile)
		}
		measured++
	})

	if measured != 3 {
		t.Fatalf("measured %d real-layout sessions, want 3", measured)
	}
}

// TestRealLayoutFailsLoud pins the rename and corruption mutations for
// the shipped layout. Every broken file errors naming that file; none
// skips.
func TestRealLayoutFailsLoud(t *testing.T) {
	const clipRate = 22050
	const provRate = 24000
	clipA := syllables(22050, clipRate, 1)
	clipB := syllables(22050, clipRate, 2)
	build := func(t *testing.T) string {
		t.Helper()
		dir := t.TempDir()
		writeRealLayoutSession(t, dir, clipA, clipB, clipRate, provRate, provRate, provRate*3, 1.0, 3.0)
		return dir
	}
	cases := []struct {
		name    string
		breakIt func(t *testing.T, dir string)
		want    string
	}{
		{"no-recording", func(t *testing.T, dir string) {
			t.Helper()
			if err := os.Remove(filepath.Join(dir, "recording.ogg")); err != nil {
				t.Fatalf("remove recording.ogg: %v", err)
			}
		}, "recording.ogg"},
		{"bad-events", func(t *testing.T, dir string) {
			t.Helper()
			if err := os.WriteFile(filepath.Join(dir, "events.json"), []byte("{broken"), 0o600); err != nil {
				t.Fatalf("corrupt events.json: %v", err)
			}
		}, "events.json"},
		{"dropped-chunks", func(t *testing.T, dir string) {
			t.Helper()
			if err := os.WriteFile(filepath.Join(dir, "metadata.json"), []byte(`{"channels":2,"dropped_chunks":2}`), 0o600); err != nil {
				t.Fatalf("rewrite metadata.json: %v", err)
			}
		}, "metadata.json"},
		{"one-clip", func(t *testing.T, dir string) {
			t.Helper()
			if err := os.Remove(filepath.Join(dir, "user-b.wav")); err != nil {
				t.Fatalf("remove user-b.wav: %v", err)
			}
		}, "guest clips"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := build(t)
			tc.breakIt(t, dir)
			_, _, err := locateSessionClips(dir)
			if err == nil {
				t.Fatalf("%s measured, want a layout error", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("%s err = %v, want it to name %s", tc.name, err, tc.want)
			}
		})
	}
}
