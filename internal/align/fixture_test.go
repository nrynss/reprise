// Fixture sessions arrive as 16 bit PCM WAV. The server stores the
// provider recording as OGG, so the fixture owner transcodes it to WAV
// before placing files. The driver decodes WAV only and rejects the stored
// bytes. A transcode fidelity check against recorded fixtures stays owed
// until they land.
package align

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
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
// wants user.wav and host.wav beside provider.wav, a stereo recording with
// the user on the left channel and the host on the right, plus the recorder
// starts. A missing or unreadable wanted file is an error, never a skip, so
// a misnamed fixture file fails loudly instead of passing silently. The
// caller applies the drift verdict to the returned report.
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
	providerBytes, err := read("provider.wav")
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
	provider, err := DecodeWAV(providerBytes)
	if err != nil {
		return Report{}, fmt.Errorf("session %s provider.wav: %w", dir, err)
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

// checkFixtureSession measures one recorded session directory and fails the
// session past the drift limit. Layout errors fail too, because an existing
// directory with a wrong layout is a broken fixture, not a session from
// another pass.
func checkFixtureSession(t *testing.T, dir string) {
	t.Helper()
	report, err := measureSession(dir)
	if err != nil {
		t.Fatalf("session %s: %v", dir, err)
	}
	if !report.Pass() {
		t.Fatalf("session %s drifted by %.1f ms: %s", dir, report.DriftSec*1000, report.Warning())
	}
}

// TestFixtureSessions runs the alignment proof against every recorded
// session under testdata/sessions. Recorded fixtures do not exist yet,
// so the suite skips cleanly until they land. Once they land the count
// pins the criterion: exactly three sessions must measure.
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
			checkFixtureSession(t, filepath.Join(root, entry.Name()))
		})
	}
	if measured != 3 {
		t.Fatalf("measured %d sessions under %s, want 3", measured, root)
	}
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

// TestDriverRejectsStoredBytes pins the transcoding dependency. The stored
// provider bytes use the OGG container, so the WAV driver must reject them
// instead of measuring. Feeding stored bytes to the driver errors on magic
// by construction.
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
