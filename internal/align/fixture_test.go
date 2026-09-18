package align

import (
	"encoding/json"
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

// checkFixtureSession measures one recorded session directory. It wants
// user.wav and host.wav beside provider.wav, a stereo recording with the
// user on the left channel and the host on the right, plus the recorder
// starts. A directory without that layout is a session from another
// pass, so it skips instead of failing.
func checkFixtureSession(t *testing.T, dir string) {
	t.Helper()
	read := func(name string) []byte {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Skipf("session %s has no %s: %v", dir, name, err)
		}
		return data
	}
	userRec, err := DecodeWAV(read("user.wav"))
	if err != nil {
		t.Fatalf("session %s user.wav: %v", dir, err)
	}
	hostRec, err := DecodeWAV(read("host.wav"))
	if err != nil {
		t.Fatalf("session %s host.wav: %v", dir, err)
	}
	provider, err := DecodeWAV(read("provider.wav"))
	if err != nil {
		t.Fatalf("session %s provider.wav: %v", dir, err)
	}
	if len(provider.Channels) != 2 {
		t.Fatalf("session %s provider has %d channels, want stereo", dir, len(provider.Channels))
	}
	raw := read(offsetsFile)
	var offsets sessionOffsets
	if err := json.Unmarshal(raw, &offsets); err != nil {
		t.Fatalf("session %s offsets: %v", dir, err)
	}
	measure := func(stem Recording, channel []float64) Lag {
		t.Helper()
		resampled, err := ResampleLinear(stem.Channels[0], stem.SampleRate, provider.SampleRate)
		if err != nil {
			t.Fatalf("session %s resample: %v", dir, err)
		}
		n := len(resampled)
		if len(channel) < n {
			n = len(channel)
		}
		width, err := SearchSamples(provider.SampleRate, 0.5)
		if err != nil {
			t.Fatalf("session %s search width: %v", dir, err)
		}
		lag, err := Correlate(channel[:n], resampled[:n], provider.SampleRate, width)
		if err != nil {
			t.Fatalf("session %s correlate: %v", dir, err)
		}
		return lag
	}
	clock := ClockOffsets(offsets)
	report := Align(clock, measure(userRec, provider.Channels[0]), measure(hostRec, provider.Channels[1]))
	if !report.Pass() {
		t.Fatalf("session %s drifted by %.1f ms: %s", dir, report.DriftSec*1000, report.Warning())
	}
}

// TestFixtureSessions runs the alignment proof against every recorded
// session under testdata/sessions. Recorded fixtures do not exist yet,
// so the suite skips cleanly until they land.
func TestFixtureSessions(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "sessions")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Skipf("no recorded sessions at %s: %v", root, err)
	}
	ran := false
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		ran = true
		t.Run(entry.Name(), func(t *testing.T) {
			checkFixtureSession(t, filepath.Join(root, entry.Name()))
		})
	}
	if !ran {
		t.Skipf("no sessions under %s", root)
	}
}
