package render

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"
)

// RangeMs is one half-open millisecond range on the episode clock. Start
// is inclusive and End is exclusive. Word spans convert with the first
// word start inclusive and the last word end exclusive, so abutting words
// never double count a millisecond.
type RangeMs struct {
	// Start is the inclusive range start in milliseconds.
	Start int64
	// End is the exclusive range end in milliseconds.
	End int64
}

// Len returns the range length in milliseconds. It never goes negative,
// because every constructor rejects an end before its start.
func (r RangeMs) Len() int64 {
	if r.End < r.Start {
		return 0
	}
	return r.End - r.Start
}

// Input carries everything one render depends on. Equal inputs hash
// equal, so the hash names the output.
type Input struct {
	// UserSHA256 digests the user stem bytes.
	UserSHA256 [32]byte
	// HostSHA256 digests the host stem bytes.
	HostSHA256 [32]byte
	// UserOffsetMs shifts the user stem to the episode clock.
	UserOffsetMs int64
	// HostOffsetMs shifts the host stem to the episode clock.
	HostOffsetMs int64
	// Cuts carries the accepted removals on the episode clock.
	Cuts []RangeMs
	// ColdOpen carries the opening span on the episode clock. Nil means
	// the draft proposed none and the episode plays without one.
	ColdOpen *RangeMs
	// DurationMs bounds the episode clock.
	DurationMs int64
	// CrossfadeMs is the fade length at a join in milliseconds.
	CrossfadeMs int64
	// MixRateHz is the sample rate of the intermediate mix.
	MixRateHz int
	// OpusBitrate names the streaming encoder setting.
	OpusBitrate string
	// AACBitrate names the export encoder setting.
	AACBitrate string
}

// HashInputs names the render for its inputs. Cuts sort before hashing,
// so decision row order never changes the name.
func HashInputs(in Input) string {
	cuts := append([]RangeMs(nil), in.Cuts...)
	sort.Slice(cuts, func(i, j int) bool {
		if cuts[i].Start != cuts[j].Start {
			return cuts[i].Start < cuts[j].Start
		}
		return cuts[i].End < cuts[j].End
	})
	var b strings.Builder
	fmt.Fprintf(&b, "user:%x\n", in.UserSHA256)
	fmt.Fprintf(&b, "host:%x\n", in.HostSHA256)
	fmt.Fprintf(&b, "offsets:%d:%d\n", in.UserOffsetMs, in.HostOffsetMs)
	fmt.Fprintf(&b, "duration:%d\n", in.DurationMs)
	fmt.Fprintf(&b, "target:%g:%g:%g\n", TargetLUFS, TruePeakDBTP, LoudnessRangeLU)
	fmt.Fprintf(&b, "gap:%d\n", ColdOpenGap.Milliseconds())
	fmt.Fprintf(&b, "crossfade:%d\n", in.CrossfadeMs)
	fmt.Fprintf(&b, "mixrate:%d\n", in.MixRateHz)
	fmt.Fprintf(&b, "opus:%s\n", in.OpusBitrate)
	fmt.Fprintf(&b, "aac:%s\n", in.AACBitrate)
	for _, c := range cuts {
		fmt.Fprintf(&b, "cut:%d:%d\n", c.Start, c.End)
	}
	if in.ColdOpen != nil {
		fmt.Fprintf(&b, "cold:%d:%d\n", in.ColdOpen.Start, in.ColdOpen.End)
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

// MergeRanges sorts ranges by start and joins ranges that touch or
// overlap. The input slice stays untouched, so callers keep their order.
func MergeRanges(ranges []RangeMs) []RangeMs {
	sorted := append([]RangeMs(nil), ranges...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Start != sorted[j].Start {
			return sorted[i].Start < sorted[j].Start
		}
		return sorted[i].End < sorted[j].End
	})
	merged := make([]RangeMs, 0, len(sorted))
	for _, r := range sorted {
		n := len(merged) - 1
		if n >= 0 && r.Start <= merged[n].End {
			if r.End > merged[n].End {
				merged[n].End = r.End
			}
			continue
		}
		merged = append(merged, r)
	}
	return merged
}

// KeepRanges returns the parts of the episode clock the cuts leave. Cuts
// outside the clock clip away, and touching cuts merge first. An empty
// result means the cuts remove every millisecond.
func KeepRanges(durationMs int64, cuts []RangeMs) []RangeMs {
	if durationMs <= 0 {
		return nil
	}
	merged := MergeRanges(cuts)
	var kept []RangeMs
	cursor := int64(0)
	for _, c := range merged {
		start := max(c.Start, 0)
		end := min(c.End, durationMs)
		if end <= cursor {
			continue
		}
		if start > cursor {
			kept = append(kept, RangeMs{Start: cursor, End: min(start, durationMs)})
		}
		cursor = max(cursor, end)
	}
	if cursor < durationMs {
		kept = append(kept, RangeMs{Start: cursor, End: durationMs})
	}
	return kept
}

// IntersectRanges returns the overlap of span with each kept range. The
// cold open keeps only the milliseconds the cuts also keep, so removed
// audio never returns through the opening.
func IntersectRanges(span RangeMs, kept []RangeMs) []RangeMs {
	var out []RangeMs
	for _, k := range kept {
		start := max(span.Start, k.Start)
		end := min(span.End, k.End)
		if end > start {
			out = append(out, RangeMs{Start: start, End: end})
		}
	}
	return out
}

// JoinFades returns one fade length per join between neighbours. A join
// carries the crossfade only when both neighbours last at least that
// long, and zero, which means a hard cut, otherwise.
func JoinFades(kept []RangeMs) []time.Duration {
	fades := make([]time.Duration, max(len(kept)-1, 0))
	for i := range fades {
		if kept[i].Len() >= Crossfade.Milliseconds() && kept[i+1].Len() >= Crossfade.Milliseconds() {
			fades[i] = Crossfade
		}
	}
	return fades
}

// seconds renders a millisecond clock value as decimal seconds at
// microsecond precision, which is the precision the tool parses
// durations at. Negative values clamp to zero, because a range never
// starts before the clock.
func seconds(ms int64) string {
	if ms < 0 {
		ms = 0
	}
	return fmt.Sprintf("%d.%06d", ms/1000, (ms%1000)*1000)
}
