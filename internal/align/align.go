package align

import (
	"errors"
	"fmt"
	"math"
)

// DriftLimitSeconds is the largest relative drift the episode check
// accepts. A wider gap fails the check and shows a warning.
const DriftLimitSeconds = 0.04

// Sentinels, one per failure condition.
var (
	// ErrSignal reports a signal too short or too empty to measure.
	ErrSignal = errors.New("align: signal empty or shorter than the search window")
	// ErrSampleRate reports a sample rate that is not positive.
	ErrSampleRate = errors.New("align: sample rate must be positive")
	// ErrSearchRange reports a search range that is not positive.
	ErrSearchRange = errors.New("align: search range must be positive")
)

// ClockOffsets holds both stem starts on the shared capture clock in
// seconds. The recorder stamps each captured block and the player stamps
// each played block, so both numbers come from one clock.
type ClockOffsets struct {
	// UserStartSec is the shared clock reading at the first user sample.
	UserStartSec float64
	// HostStartSec is the shared clock reading at the first host sample.
	HostStartSec float64
}

// Origin returns the episode zero, the earlier of the two stem starts.
// Every stored offset counts forward from here.
func (o ClockOffsets) Origin() float64 {
	if o.HostStartSec < o.UserStartSec {
		return o.HostStartSec
	}
	return o.UserStartSec
}

// StemOffset returns how far a stem start sits past the episode zero.
func StemOffset(startSec, originSec float64) float64 {
	return startSec - originSec
}

// RecorderGap returns the host start minus the user start. Correlation
// reproduces this gap from the provider recording when the stems hold.
func (o ClockOffsets) RecorderGap() float64 {
	return o.HostStartSec - o.UserStartSec
}

// Lag is the measured delay between a provider reference channel and a
// local stem at one sample rate.
type Lag struct {
	// Samples is the integer delay. Positive means the stem runs later
	// than the reference channel by this many samples.
	Samples int
	// Seconds is Samples expressed in seconds at the measured rate.
	Seconds float64
}

// Correlate finds the integer delay between reference and candidate by
// normalized cross correlation over lags from minus maxLag to plus
// maxLag. Positive lag means the candidate runs later than the
// reference. Both slices must share one sample rate and each must hold
// more than maxLag samples. The first maximum wins when two lags tie.
func Correlate(reference, candidate []float64, sampleRate, maxLag int) (Lag, error) {
	if sampleRate <= 0 {
		return Lag{}, fmt.Errorf("align: rate %d: %w", sampleRate, ErrSampleRate)
	}
	if maxLag <= 0 {
		return Lag{}, fmt.Errorf("align: range %d: %w", maxLag, ErrSearchRange)
	}
	if len(reference) <= maxLag || len(candidate) <= maxLag {
		return Lag{}, fmt.Errorf(
			"align: lengths %d and %d with range %d: %w",
			len(reference), len(candidate), maxLag, ErrSignal,
		)
	}
	best := 0
	bestScore := math.Inf(-1)
	for lag := -maxLag; lag <= maxLag; lag++ {
		start := 0
		if -lag > start {
			start = -lag
		}
		end := len(reference)
		if len(candidate)-lag < end {
			end = len(candidate) - lag
		}
		if end <= start {
			continue
		}
		var dot, energyRef, energyCand float64
		for i := start; i < end; i++ {
			r := reference[i]
			c := candidate[i+lag]
			dot += r * c
			energyRef += r * r
			energyCand += c * c
		}
		if energyRef == 0 || energyCand == 0 {
			continue
		}
		score := dot / math.Sqrt(energyRef*energyCand)
		if score > bestScore {
			bestScore = score
			best = lag
		}
	}
	if math.IsInf(bestScore, -1) {
		return Lag{}, fmt.Errorf("align: no nonzero overlap: %w", ErrSignal)
	}
	return Lag{Samples: best, Seconds: float64(best) / float64(sampleRate)}, nil
}

// SearchSamples converts a search width in seconds to samples, rounding
// up so the window always covers the requested width.
func SearchSamples(sampleRate int, seconds float64) (int, error) {
	if sampleRate <= 0 {
		return 0, fmt.Errorf("align: rate %d: %w", sampleRate, ErrSampleRate)
	}
	if seconds <= 0 {
		return 0, fmt.Errorf("align: width %v: %w", seconds, ErrSearchRange)
	}
	return int(math.Ceil(float64(sampleRate) * seconds)), nil
}

// ResampleLinear converts samples from one rate to another by linear
// interpolation. Equal rates return a copy. When the output length divides
// the input span evenly the output keeps both endpoints, as in an exact
// doubling. Other ratios truncate the tail by integer division, so the last
// input sample may never emit.
func ResampleLinear(samples []float64, fromRate, toRate int) ([]float64, error) {
	if len(samples) == 0 {
		return nil, fmt.Errorf("align: no samples: %w", ErrSignal)
	}
	if fromRate <= 0 || toRate <= 0 {
		return nil, fmt.Errorf("align: rates %d to %d: %w", fromRate, toRate, ErrSampleRate)
	}
	if fromRate == toRate {
		out := make([]float64, len(samples))
		copy(out, samples)
		return out, nil
	}
	outLen := (len(samples)-1)*toRate/fromRate + 1
	out := make([]float64, outLen)
	for i := range out {
		pos := float64(i) * float64(fromRate) / float64(toRate)
		low := int(math.Floor(pos))
		high := low + 1
		if high > len(samples)-1 {
			high = len(samples) - 1
		}
		frac := pos - float64(low)
		out[i] = samples[low]*(1-frac) + samples[high]*frac
	}
	return out, nil
}

// StemResult carries one stem from its recorder offset to its measured
// offset on the episode clock.
type StemResult struct {
	// RecorderOffsetSec counts from the episode zero to the stem start
	// the shared clock reported.
	RecorderOffsetSec float64
	// MeasuredOffsetSec counts from the episode zero to the stem start
	// the provider correlation confirms.
	MeasuredOffsetSec float64
	// DriftSec is the measured offset minus the recorder offset.
	DriftSec float64
}

// Report places both stems on the episode clock and proves the
// placement. The provider stereo channels share one container clock, so
// the gap between the two lags cancels the provider path latency and
// must reproduce the recorder gap.
type Report struct {
	// OriginSec is the episode zero both offsets count from.
	OriginSec float64
	// User is the user stem from recorder start to measured start.
	User StemResult
	// Host is the host stem from recorder start to measured start.
	Host StemResult
	// RecorderGapSec is the host recorder start minus the user one.
	RecorderGapSec float64
	// MeasuredGapSec is the host measured start minus the user one.
	MeasuredGapSec float64
	// DriftSec is the measured gap minus the recorder gap.
	DriftSec float64
}

// Pass reports whether the relative drift sits within the limit.
func (r Report) Pass() bool {
	return math.Abs(r.DriftSec) <= DriftLimitSeconds
}

// Warning describes a failed check for display. It returns an empty
// string when the stems hold.
func (r Report) Warning() string {
	if r.Pass() {
		return ""
	}
	return fmt.Sprintf(
		"align: stems drift by %.1f ms, above the %.0f ms limit",
		r.DriftSec*1000, DriftLimitSeconds*1000,
	)
}

// Align combines recorder clock offsets with one correlation lag per
// stem into a report. Each lag must come from its own channel of the
// same provider recording at one shared sample rate.
func Align(clock ClockOffsets, userLag, hostLag Lag) Report {
	origin := clock.Origin()
	userRecorder := StemOffset(clock.UserStartSec, origin)
	hostRecorder := StemOffset(clock.HostStartSec, origin)
	userMeasured := userRecorder + userLag.Seconds
	hostMeasured := hostRecorder + hostLag.Seconds
	report := Report{
		OriginSec:      origin,
		RecorderGapSec: clock.RecorderGap(),
		MeasuredGapSec: hostMeasured - userMeasured,
		User:           StemResult{RecorderOffsetSec: userRecorder, MeasuredOffsetSec: userMeasured, DriftSec: userLag.Seconds},
		Host:           StemResult{RecorderOffsetSec: hostRecorder, MeasuredOffsetSec: hostMeasured, DriftSec: hostLag.Seconds},
	}
	report.DriftSec = report.MeasuredGapSec - report.RecorderGapSec
	return report
}
