package align

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// Sentinels for recorded file decoding, one per failure condition.
var (
	// ErrWAV reports bytes that are not a readable WAV file.
	ErrWAV = errors.New("align: unreadable wav")
	// ErrFormat reports a WAV encoding this package does not measure,
	// anything but mono or stereo PCM with 16 bits per sample.
	ErrFormat = errors.New("align: unsupported wav format")
)

// Recording holds decoded mono or stereo PCM normalized to the range
// minus one to one, with the sample rate the header declares.
type Recording struct {
	// Channels holds one slice per channel. A stereo provider recording
	// carries the user on index zero and the host on index one.
	Channels [][]float64
	// SampleRate is the header rate in hertz.
	SampleRate int
}

// DecodeWAV decodes 16 bit PCM WAV bytes into a Recording. It reads the
// data the buffer actually holds, so files with placeholder sizes still
// decode. It rejects anything but mono or stereo PCM.
func DecodeWAV(data []byte) (Recording, error) {
	if len(data) < 44 {
		return Recording{}, fmt.Errorf("align: %d bytes: %w", len(data), ErrWAV)
	}
	if string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return Recording{}, fmt.Errorf("align: bad magic: %w", ErrWAV)
	}
	pos := 12
	var channels, rate, bits int
	var haveFmt bool
	var pcm []byte
	for pos+8 <= len(data) {
		id := string(data[pos : pos+4])
		size := int(binary.LittleEndian.Uint32(data[pos+4 : pos+8]))
		body := pos + 8
		end := body + size
		if end > len(data) {
			end = len(data)
		}
		switch id {
		case "fmt ":
			if end-body < 16 {
				return Recording{}, fmt.Errorf("align: short fmt: %w", ErrWAV)
			}
			format := binary.LittleEndian.Uint16(data[body : body+2])
			if format != 1 {
				return Recording{}, fmt.Errorf("align: format %d: %w", format, ErrFormat)
			}
			channels = int(binary.LittleEndian.Uint16(data[body+2 : body+4]))
			rate = int(binary.LittleEndian.Uint32(data[body+4 : body+8]))
			bits = int(binary.LittleEndian.Uint16(data[body+14 : body+16]))
			haveFmt = true
		case "data":
			pcm = data[body:end]
		}
		pos = end
		if size%2 == 1 && pos < len(data) {
			pos++
		}
	}
	if !haveFmt {
		return Recording{}, fmt.Errorf("align: no fmt chunk: %w", ErrWAV)
	}
	if channels < 1 || channels > 2 {
		return Recording{}, fmt.Errorf("align: %d channels: %w", channels, ErrFormat)
	}
	if bits != 16 {
		return Recording{}, fmt.Errorf("align: %d bits: %w", bits, ErrFormat)
	}
	if rate <= 0 {
		return Recording{}, fmt.Errorf("align: rate %d: %w", rate, ErrSampleRate)
	}
	frames := len(pcm) / (channels * 2)
	if frames == 0 {
		return Recording{}, fmt.Errorf("align: no frames: %w", ErrWAV)
	}
	out := make([][]float64, channels)
	for c := range out {
		out[c] = make([]float64, frames)
	}
	for f := 0; f < frames; f++ {
		for c := 0; c < channels; c++ {
			raw := int16(binary.LittleEndian.Uint16(pcm[(f*channels+c)*2:]))
			out[c][f] = float64(raw) / 32768
		}
	}
	return Recording{Channels: out, SampleRate: rate}, nil
}

// EncodeWAV builds 16 bit PCM WAV bytes from channel slices. Tests use
// it to write the exact bytes DecodeWAV must read back. All channels
// must share one length.
func EncodeWAV(channels [][]float64, sampleRate int) ([]byte, error) {
	if len(channels) == 0 || len(channels) > 2 {
		return nil, fmt.Errorf("align: %d channels: %w", len(channels), ErrFormat)
	}
	if sampleRate <= 0 {
		return nil, fmt.Errorf("align: rate %d: %w", sampleRate, ErrSampleRate)
	}
	frames := len(channels[0])
	for _, ch := range channels {
		if len(ch) != frames {
			return nil, fmt.Errorf("align: ragged channels: %w", ErrWAV)
		}
	}
	head := make([]byte, 44)
	copy(head[0:4], "RIFF")
	binary.LittleEndian.PutUint32(head[4:8], uint32(36+frames*len(channels)*2))
	copy(head[8:12], "WAVE")
	copy(head[12:16], "fmt ")
	binary.LittleEndian.PutUint32(head[16:20], 16)
	binary.LittleEndian.PutUint16(head[20:22], 1)
	binary.LittleEndian.PutUint16(head[22:24], uint16(len(channels)))
	binary.LittleEndian.PutUint32(head[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(head[28:32], uint32(sampleRate*len(channels)*2))
	binary.LittleEndian.PutUint16(head[32:34], uint16(len(channels)*2))
	binary.LittleEndian.PutUint16(head[34:36], 16)
	copy(head[36:40], "data")
	binary.LittleEndian.PutUint32(head[40:44], uint32(frames*len(channels)*2))
	body := make([]byte, frames*len(channels)*2)
	for f := 0; f < frames; f++ {
		for c := range channels {
			v := channels[c][f]
			if v > 1 {
				v = 1
			}
			if v < -1 {
				v = -1
			}
			binary.LittleEndian.PutUint16(body[(f*len(channels)+c)*2:], uint16(int16(math.Round(v*32767))))
		}
	}
	return append(head, body...), nil
}
