package align

import (
	"errors"
	"math"
	"testing"
)

// chirp builds a swept tone with a gentle decay. The sweep keeps the
// correlation peak unique, because a pure tone repeats every period and
// a shifted copy can outscore the true lag on rounding noise.
func chirp(n int, f0, f1, rate float64) []float64 {
	out := make([]float64, n)
	dur := float64(n) / rate
	for i := range out {
		t := float64(i) / rate
		phase := 2 * math.Pi * (f0*t + (f1-f0)*t*t/(2*dur))
		out[i] = math.Sin(phase) * math.Exp(-t/(dur*0.7))
	}
	return out
}

// delayed pads sig with zeros at the front, so the copy runs late by
// delay samples at the same length.
func delayed(sig []float64, delay int) []float64 {
	out := make([]float64, len(sig))
	copy(out[delay:], sig[:len(sig)-delay])
	return out
}

func TestCorrelateFindsKnownDelay(t *testing.T) {
	const rate = 8000
	ref := chirp(4000, 300, 1500, rate)
	const delay = 137
	lag, err := Correlate(ref, delayed(ref, delay), rate, 500)
	if err != nil {
		t.Fatalf("Correlate returned %v", err)
	}
	if lag.Samples != delay {
		t.Fatalf("lag samples = %d, want %d", lag.Samples, delay)
	}
	if lag.Seconds != float64(delay)/float64(rate) {
		t.Fatalf("lag seconds = %v, want %v", lag.Seconds, float64(delay)/float64(rate))
	}
}

func TestCorrelateFindsNegativeDelay(t *testing.T) {
	const rate = 8000
	base := chirp(4000, 300, 1500, rate)
	ref := delayed(base, 200)
	lag, err := Correlate(ref, delayed(base, 100), rate, 500)
	if err != nil {
		t.Fatalf("Correlate returned %v", err)
	}
	if lag.Samples != -100 {
		t.Fatalf("lag samples = %d, want -100", lag.Samples)
	}
}

func TestCorrelateZeroDelay(t *testing.T) {
	const rate = 8000
	ref := chirp(4000, 300, 1500, rate)
	lag, err := Correlate(ref, ref, rate, 500)
	if err != nil {
		t.Fatalf("Correlate returned %v", err)
	}
	if lag.Samples != 0 || lag.Seconds != 0 {
		t.Fatalf("lag = %+v, want zero", lag)
	}
}

func TestCorrelateRejectsBadInput(t *testing.T) {
	ref := chirp(100, 300, 1500, 8000)
	cases := []struct {
		name      string
		reference []float64
		candidate []float64
		rate      int
		maxLag    int
		want      error
	}{
		{"empty reference", nil, ref, 8000, 10, ErrSignal},
		{"empty candidate", ref, nil, 8000, 10, ErrSignal},
		{"shorter than window", ref[:10], ref[:10], 8000, 10, ErrSignal},
		{"silent candidate", ref, make([]float64, len(ref)), 8000, 10, ErrSignal},
		{"bad rate", ref, ref, 0, 10, ErrSampleRate},
		{"bad range", ref, ref, 8000, 0, ErrSearchRange},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Correlate(tc.reference, tc.candidate, tc.rate, tc.maxLag)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestSearchSamplesRoundsUp(t *testing.T) {
	got, err := SearchSamples(8000, 0.04)
	if err != nil {
		t.Fatalf("SearchSamples returned %v", err)
	}
	if got != 320 {
		t.Fatalf("search samples = %d, want 320", got)
	}
	if _, err := SearchSamples(0, 0.04); !errors.Is(err, ErrSampleRate) {
		t.Fatalf("bad rate err = %v", err)
	}
	if _, err := SearchSamples(8000, 0); !errors.Is(err, ErrSearchRange) {
		t.Fatalf("bad width err = %v", err)
	}
}

func TestResampleLinearExact(t *testing.T) {
	up, err := ResampleLinear([]float64{0, 10}, 1, 2)
	if err != nil {
		t.Fatalf("ResampleLinear returned %v", err)
	}
	wantUp := []float64{0, 5, 10}
	if len(up) != len(wantUp) {
		t.Fatalf("up length = %d, want %d", len(up), len(wantUp))
	}
	for i := range wantUp {
		if up[i] != wantUp[i] {
			t.Fatalf("up[%d] = %v, want %v", i, up[i], wantUp[i])
		}
	}
	down, err := ResampleLinear([]float64{0, 5, 10}, 2, 1)
	if err != nil {
		t.Fatalf("ResampleLinear returned %v", err)
	}
	wantDown := []float64{0, 10}
	if len(down) != len(wantDown) || down[0] != wantDown[0] || down[1] != wantDown[1] {
		t.Fatalf("down = %v, want %v", down, wantDown)
	}
	same, err := ResampleLinear([]float64{3, 4}, 8000, 8000)
	if err != nil {
		t.Fatalf("ResampleLinear returned %v", err)
	}
	if len(same) != 2 || same[0] != 3 || same[1] != 4 {
		t.Fatalf("same rate = %v, want a copy of input", same)
	}
	if _, err := ResampleLinear(nil, 8000, 16000); !errors.Is(err, ErrSignal) {
		t.Fatalf("empty err = %v", err)
	}
	if _, err := ResampleLinear([]float64{1}, 0, 16000); !errors.Is(err, ErrSampleRate) {
		t.Fatalf("rate err = %v", err)
	}
}

func TestClockOffsets(t *testing.T) {
	clock := ClockOffsets{UserStartSec: 0.5, HostStartSec: 1.5}
	if clock.Origin() != 0.5 {
		t.Fatalf("origin = %v, want 0.5", clock.Origin())
	}
	if StemOffset(clock.HostStartSec, clock.Origin()) != 1.0 {
		t.Fatalf("host offset = %v, want 1.0", StemOffset(clock.HostStartSec, clock.Origin()))
	}
	if clock.RecorderGap() != 1.0 {
		t.Fatalf("recorder gap = %v, want 1.0", clock.RecorderGap())
	}
	flipped := ClockOffsets{UserStartSec: 2.0, HostStartSec: 1.0}
	if flipped.Origin() != 1.0 {
		t.Fatalf("flipped origin = %v, want 1.0", flipped.Origin())
	}
}

func TestAlignPassesWhenGapsMatch(t *testing.T) {
	clock := ClockOffsets{UserStartSec: 0.5, HostStartSec: 1.5}
	userLag := Lag{Samples: 500, Seconds: 0.0625}
	hostLag := Lag{Samples: 1000, Seconds: 0.125}
	report := Align(clock, userLag, hostLag)
	if report.OriginSec != 0.5 {
		t.Fatalf("origin = %v, want 0.5", report.OriginSec)
	}
	if report.User.RecorderOffsetSec != 0 || report.Host.RecorderOffsetSec != 1.0 {
		t.Fatalf("recorder offsets = %v and %v, want 0 and 1.0",
			report.User.RecorderOffsetSec, report.Host.RecorderOffsetSec)
	}
	if report.User.MeasuredOffsetSec != 0.0625 || report.Host.MeasuredOffsetSec != 1.125 {
		t.Fatalf("measured offsets = %v and %v, want 0.0625 and 1.125",
			report.User.MeasuredOffsetSec, report.Host.MeasuredOffsetSec)
	}
	if report.DriftSec != 0.0625 {
		t.Fatalf("drift = %v, want 0.0625", report.DriftSec)
	}
	if report.Pass() {
		t.Fatalf("drift above the limit passed")
	}
	if report.Warning() == "" {
		t.Fatalf("failed check gave no warning")
	}
}

func TestAlignCancelsCommonLatency(t *testing.T) {
	clock := ClockOffsets{UserStartSec: 0.5, HostStartSec: 1.5}
	lag := Lag{Samples: 800, Seconds: 0.1}
	report := Align(clock, lag, lag)
	if report.DriftSec != 0 {
		t.Fatalf("drift = %v, want 0", report.DriftSec)
	}
	if !report.Pass() {
		t.Fatalf("matching lags failed")
	}
	if report.Warning() != "" {
		t.Fatalf("passing check warned: %q", report.Warning())
	}
}

func TestDriftLimitBoundary(t *testing.T) {
	if DriftLimitSeconds != 0.04 {
		t.Fatalf("drift limit = %v, want 0.04", DriftLimitSeconds)
	}
	clock := ClockOffsets{}
	inside := Align(clock, Lag{}, Lag{Samples: 240, Seconds: 0.03})
	if !inside.Pass() {
		t.Fatalf("30 ms drift failed")
	}
	outside := Align(clock, Lag{}, Lag{Samples: 400, Seconds: 0.05})
	if outside.Pass() {
		t.Fatalf("50 ms drift passed")
	}
	exact := Align(clock, Lag{}, Lag{Samples: 320, Seconds: 0.04})
	if !exact.Pass() {
		t.Fatalf("40 ms drift failed, the limit includes the boundary")
	}
	if exact.Warning() != "" {
		t.Fatalf("boundary check warned: %q", exact.Warning())
	}
}

// TestAlignEndToEndOnSyntheticSession mirrors the fixture driver without
// files. Two delayed copies play the local stems, their mix plays the
// provider stereo, and the measured gap must reproduce the clock gap.
func TestAlignEndToEndOnSyntheticSession(t *testing.T) {
	const rate = 8000
	user := chirp(6000, 300, 1500, rate)
	host := chirp(6000, 1500, 300, rate)
	const pathLatency = 150
	provider := [][]float64{delayed(user, pathLatency), delayed(host, pathLatency)}
	userLag, err := Correlate(provider[0], user, rate, 500)
	if err != nil {
		t.Fatalf("user Correlate returned %v", err)
	}
	hostLag, err := Correlate(provider[1], host, rate, 500)
	if err != nil {
		t.Fatalf("host Correlate returned %v", err)
	}
	if userLag.Samples != -pathLatency || hostLag.Samples != -pathLatency {
		t.Fatalf("lags = %d and %d, want %d and %d",
			userLag.Samples, hostLag.Samples, -pathLatency, -pathLatency)
	}
	clock := ClockOffsets{UserStartSec: 0.25, HostStartSec: 1.0}
	report := Align(clock, userLag, hostLag)
	if !report.Pass() {
		t.Fatalf("synthetic session drifted by %v", report.DriftSec)
	}
}
