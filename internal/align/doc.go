// Package align places both session stems on one episode clock and proves
// the placement by measurement.
//
// The recorder and the player share one audio clock, so each stem carries
// its start time on that clock. Correlation checks the starts against the
// provider stereo recording, which carries the user on the left channel
// and the host on the right. Every function here is pure. Slices go in
// and numbers come out, so tests assert exact sample math on generated
// signals with no device and no clock.
package align
