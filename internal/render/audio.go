package render

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/nrynss/keel/ffmpeg"
)

// assembly is one planned splice. The graph ends at the label [asm].
// Boundaries hold every join position in render-clock seconds, so a click
// probe can measure each one separately from the rest.
type assembly struct {
	// graph is the filter graph applied to the mix input.
	graph string
	// boundaries holds each join time in render-clock seconds.
	boundaries []float64
}

// planAssembly lays the cold open first, then the gap, then the episode.
// Cold ranges duplicate kept milliseconds, so removed audio never
// returns through the opening. Every join carries the crossfade when
// both sides last at least that long, and a hard cut otherwise. Boundary
// times account for each fade overlap, because an acrossfade lasts the
// sum of both sides minus the fade.
func planAssembly(kept, cold []RangeMs) assembly {
	if len(kept) == 0 {
		return assembly{}
	}
	var b strings.Builder
	trimCount := len(kept) + len(cold)
	fmt.Fprintf(&b, "[0:a]asplit=%d", trimCount)
	for i := 0; i < trimCount; i++ {
		fmt.Fprintf(&b, "[trim%d]", i)
	}
	next := 0
	if len(cold) > 0 {
		coldLabel, coldTotal, coldBounds := emitChain(&b, "cold", cold, next)
		next += len(cold)
		epLabel, epTotal, epBounds := emitChain(&b, "ep", kept, next)
		gapMs := ColdOpenGap.Milliseconds()
		fmt.Fprintf(&b, ";anullsrc=r=%d:cl=stereo:d=%s[gap]", MixRate, seconds(gapMs))
		coldFade := joinFadeMs(float64(coldTotal), float64(gapMs))
		gapFade := joinFadeMs(float64(gapMs), float64(epTotal))
		if coldFade > 0 {
			fmt.Fprintf(&b, ";[%s][gap]acrossfade=d=%s:curve1=tri:curve2=tri[cg]",
				coldLabel, seconds(coldFade))
		} else {
			fmt.Fprintf(&b, ";[%s][gap]concat=n=2:v=0:a=1[cg]", coldLabel)
		}
		if gapFade > 0 {
			fmt.Fprintf(&b, ";[cg][%s]acrossfade=d=%s:curve1=tri:curve2=tri[asm]",
				epLabel, seconds(gapFade))
		} else {
			fmt.Fprintf(&b, ";[cg][%s]concat=n=2:v=0:a=1[asm]", epLabel)
		}
		out := append([]float64(nil), coldBounds...)
		coldSec := float64(coldTotal) / 1000
		out = append(out, coldSec)
		gapJoin := coldSec + float64(gapMs-coldFade)/1000
		out = append(out, gapJoin)
		base := gapJoin - float64(gapFade)/1000
		for _, t := range epBounds {
			out = append(out, base+t)
		}
		return assembly{graph: b.String(), boundaries: out}
	}
	epLabel, _, epBounds := emitChain(&b, "ep", kept, next)
	fmt.Fprintf(&b, ";[%s]anull[asm]", epLabel)
	return assembly{graph: b.String(), boundaries: epBounds}
}

// emitChain trims every range out of its split input and joins the pieces
// in order. It returns the final label, the output length in
// milliseconds, and each join position in chain-relative seconds.
func emitChain(b *strings.Builder, prefix string, ranges []RangeMs, first int) (string, int64, []float64) {
	for i, r := range ranges {
		fmt.Fprintf(b, ";[trim%d]atrim=start=%s:end=%s,asetpts=PTS-STARTPTS[%s%d]",
			first+i, seconds(r.Start), seconds(r.End), prefix, i)
	}
	if len(ranges) == 1 {
		return fmt.Sprintf("%s0", prefix), ranges[0].Len(), nil
	}
	var bounds []float64
	acc := fmt.Sprintf("%s0", prefix)
	total := ranges[0].Len()
	clock := float64(ranges[0].Len()) / 1000
	fades := JoinFades(ranges)
	for i, fade := range fades {
		bounds = append(bounds, clock)
		clock -= float64(fade.Milliseconds()) / 1000
		out := fmt.Sprintf("%sjoin%d", prefix, i)
		if i == len(fades)-1 {
			out = fmt.Sprintf("%sfull", prefix)
		}
		if fade > 0 {
			fmt.Fprintf(b, ";[%s][%s%d]acrossfade=d=%s:curve1=tri:curve2=tri[%s]",
				acc, prefix, i+1, seconds(fade.Milliseconds()), out)
			total += ranges[i+1].Len() - fade.Milliseconds()
		} else {
			fmt.Fprintf(b, ";[%s][%s%d]concat=n=2:v=0:a=1[%s]", acc, prefix, i+1, out)
			total += ranges[i+1].Len()
		}
		clock += float64(ranges[i+1].Len()) / 1000
		acc = out
	}
	return acc, total, bounds
}

// joinFadeMs returns the crossfade milliseconds for a join of two sides,
// or zero for a hard cut.
func joinFadeMs(leftMs, rightMs float64) int64 {
	if leftMs >= float64(Crossfade.Milliseconds()) && rightMs >= float64(Crossfade.Milliseconds()) {
		return Crossfade.Milliseconds()
	}
	return 0
}

// JoinTimes lays kept and cold ranges as the assembly does and returns
// each join position in render-clock seconds. Probes measure every join
// separately from the rest through these times.
func JoinTimes(kept, cold []RangeMs) []float64 {
	return planAssembly(kept, cold).boundaries
}

// mixStems mixes both stems to one stereo file at the mix rate. Each stem
// resamples first, then shifts by its alignment offset, so both land on
// the episode clock before mixing. The mix keeps every millisecond,
// because cuts apply later on the shared clock.
func mixStems(ctx context.Context, tools ffmpeg.Tools, userPath string, userDelayMs int64, hostPath string, hostDelayMs int64, dst string) error {
	if ctx.Err() != nil {
		return fmt.Errorf("render: mix: %w", ctx.Err())
	}
	userDelayMs = max(userDelayMs, 0)
	hostDelayMs = max(hostDelayMs, 0)
	graph := fmt.Sprintf(
		"[0:a]aformat=sample_fmts=fltp:channel_layouts=stereo,aresample=%d,adelay=%d|%d[u];"+
			"[1:a]aformat=sample_fmts=fltp:channel_layouts=stereo,aresample=%d,adelay=%d|%d[h];"+
			"[u][h]amix=inputs=2:normalize=0[mix]",
		MixRate, userDelayMs, userDelayMs, MixRate, hostDelayMs, hostDelayMs)
	args := []string{"-y", "-i", userPath, "-i", hostPath,
		"-filter_complex", graph, "-map", "[mix]",
		"-c:a", "pcm_s16le", "-ar", strconv.Itoa(MixRate), "-ac", "2", dst}
	if err := ffmpeg.Run(ctx, tools, args...); err != nil {
		return fmt.Errorf("render: mix: %w", err)
	}
	return nil
}

// measurement holds the four numbers the measure pass reports. The
// strings stay verbatim, because the apply pass hands them back to the
// tool in the words it used.
type measurement struct {
	// InputI is the measured integrated loudness.
	InputI string
	// InputLRA is the measured loudness range.
	InputLRA string
	// InputTP is the measured true peak.
	InputTP string
	// InputThresh is the measured threshold.
	InputThresh string
}

// measureLoudness runs the assembly once and returns the measured
// numbers. The pass writes nothing else, so the output goes to null.
// The stats file the tool writes is the only source of numbers.
func measureLoudness(ctx context.Context, tools ffmpeg.Tools, mixPath, graph string) (measurement, error) {
	if ctx.Err() != nil {
		return measurement{}, fmt.Errorf("render: measure: %w", ctx.Err())
	}
	dir, err := os.MkdirTemp("", "reprise-measure-")
	if err != nil {
		return measurement{}, fmt.Errorf("render: measure: %w", err)
	}
	defer os.RemoveAll(dir)
	stats := filepath.Join(dir, "loudnorm.json")
	full := fmt.Sprintf("%s;[asm]loudnorm=print_format=json:stats_file=%s[stats]", graph, stats)
	args := []string{"-i", mixPath, "-filter_complex", full, "-map", "[stats]", "-f", "null", "-"}
	if err := ffmpeg.Run(ctx, tools, args...); err != nil {
		return measurement{}, fmt.Errorf("render: measure: %w", err)
	}
	raw, err := os.ReadFile(stats)
	if err != nil {
		return measurement{}, fmt.Errorf("render: measure: %w: %v", ErrNoMeasurement, err)
	}
	return parseMeasurement(raw)
}

// parseMeasurement reads the measured numbers out of the stats file the
// tool wrote. Anything unreadable, incomplete or not finite reports the
// no-measurement sentinel, because a guess would defeat the two-pass
// design.
func parseMeasurement(raw []byte) (measurement, error) {
	var parsed struct {
		InputI      string `json:"input_i"`
		InputLRA    string `json:"input_lra"`
		InputTP     string `json:"input_tp"`
		InputThresh string `json:"input_thresh"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return measurement{}, fmt.Errorf("%w: %v", ErrNoMeasurement, err)
	}
	m := measurement{
		InputI:      parsed.InputI,
		InputLRA:    parsed.InputLRA,
		InputTP:     parsed.InputTP,
		InputThresh: parsed.InputThresh,
	}
	if m.InputI == "" || m.InputLRA == "" || m.InputTP == "" || m.InputThresh == "" {
		return measurement{}, fmt.Errorf("%w: a measured number is absent", ErrNoMeasurement)
	}
	for _, s := range []string{m.InputI, m.InputLRA, m.InputTP, m.InputThresh} {
		v, err := strconv.ParseFloat(s, 64)
		if err != nil || isNonFinite(v) {
			return measurement{}, fmt.Errorf("%w: a measured number is not finite", ErrNoMeasurement)
		}
	}
	return m, nil
}

// isNonFinite reports an infinite or NaN value. Silence measures as
// negative infinity, which the apply pass cannot hand back.
func isNonFinite(v float64) bool {
	return v != v || v > 1e308 || v < -1e308
}

// encodeOutputs runs the assembly a second time with the measured
// numbers and writes both outputs in one run. The aformat stage returns
// the audio to the mix rate and stereo layout, because the normaliser
// works at its own rate inside.
func encodeOutputs(ctx context.Context, tools ffmpeg.Tools, mixPath, graph string, m measurement, opusDst, aacDst string) error {
	if ctx.Err() != nil {
		return fmt.Errorf("render: encode: %w", ctx.Err())
	}
	restore := fmt.Sprintf("aformat=sample_rates=%d:channel_layouts=stereo", MixRate)
	apply := fmt.Sprintf("loudnorm=I=%g:LRA=%g:TP=%g:measured_I=%s:measured_LRA=%s:measured_TP=%s:measured_thresh=%s:linear=true",
		TargetLUFS, LoudnessRangeLU, TruePeakDBTP, m.InputI, m.InputLRA, m.InputTP, m.InputThresh)
	full := fmt.Sprintf("%s;[asm]%s,%s,asplit=2[opus][aac]", graph, apply, restore)
	args := []string{"-y", "-i", mixPath, "-filter_complex", full,
		"-map", "[opus]", "-c:a", "libopus", "-b:a", OpusBitrate, opusDst,
		"-map", "[aac]", "-c:a", "aac", "-b:a", AACBitrate, aacDst}
	if err := ffmpeg.Run(ctx, tools, args...); err != nil {
		return fmt.Errorf("render: encode: %w", err)
	}
	return nil
}

// verifyLoudness measures the integrated loudness of a finished file
// with a fresh single pass. The apply pass aims at the target, and this
// read confirms the bytes carry it.
func verifyLoudness(ctx context.Context, tools ffmpeg.Tools, path string) (float64, error) {
	if ctx.Err() != nil {
		return 0, fmt.Errorf("render: verify: %w", ctx.Err())
	}
	dir, err := os.MkdirTemp("", "reprise-verify-")
	if err != nil {
		return 0, fmt.Errorf("render: verify: %w", err)
	}
	defer os.RemoveAll(dir)
	stats := filepath.Join(dir, "loudnorm.json")
	args := []string{"-i", path, "-af", fmt.Sprintf("loudnorm=print_format=json:stats_file=%s", stats),
		"-f", "null", "-"}
	if err := ffmpeg.Run(ctx, tools, args...); err != nil {
		return 0, fmt.Errorf("render: verify: %w", err)
	}
	raw, err := os.ReadFile(stats)
	if err != nil {
		return 0, fmt.Errorf("render: verify: %w: %v", ErrNoMeasurement, err)
	}
	m, err := parseMeasurement(raw)
	if err != nil {
		return 0, fmt.Errorf("render: verify: %w", err)
	}
	v, err := strconv.ParseFloat(m.InputI, 64)
	if err != nil {
		return 0, fmt.Errorf("render: verify loudness %q: %w", m.InputI, err)
	}
	return v, nil
}
