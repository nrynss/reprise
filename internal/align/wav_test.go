package align

import (
	"encoding/binary"
	"errors"
	"testing"
)

func TestWAVRoundTrip(t *testing.T) {
	left := []float64{0, 0.125, -0.125, 0.5, -0.5}
	right := []float64{-0.5, 0.5, 0, 0.125, -0.125}
	data, err := EncodeWAV([][]float64{left, right}, 8000)
	if err != nil {
		t.Fatalf("EncodeWAV returned %v", err)
	}
	rec, err := DecodeWAV(data)
	if err != nil {
		t.Fatalf("DecodeWAV returned %v", err)
	}
	if rec.SampleRate != 8000 {
		t.Fatalf("rate = %d, want 8000", rec.SampleRate)
	}
	if len(rec.Channels) != 2 {
		t.Fatalf("channels = %d, want 2", len(rec.Channels))
	}
	for c, want := range [][]float64{left, right} {
		for i, v := range want {
			if rec.Channels[c][i] != v {
				t.Fatalf("channel %d sample %d = %v, want %v", c, i, rec.Channels[c][i], v)
			}
		}
	}
}

func TestWAVMonoDecodes(t *testing.T) {
	data, err := EncodeWAV([][]float64{{0, 0.25, -0.25}}, 24000)
	if err != nil {
		t.Fatalf("EncodeWAV returned %v", err)
	}
	rec, err := DecodeWAV(data)
	if err != nil {
		t.Fatalf("DecodeWAV returned %v", err)
	}
	if len(rec.Channels) != 1 || rec.SampleRate != 24000 {
		t.Fatalf("decoded = %d channels at %d, want 1 at 24000", len(rec.Channels), rec.SampleRate)
	}
}

func TestWAVIgnoresPlaceholderSizes(t *testing.T) {
	data, err := EncodeWAV([][]float64{{0.125, -0.125, 0.25}}, 8000)
	if err != nil {
		t.Fatalf("EncodeWAV returned %v", err)
	}
	binary.LittleEndian.PutUint32(data[4:8], 2147479588)
	binary.LittleEndian.PutUint32(data[40:44], 2147479552)
	rec, err := DecodeWAV(data)
	if err != nil {
		t.Fatalf("DecodeWAV returned %v", err)
	}
	if len(rec.Channels[0]) != 3 {
		t.Fatalf("frames = %d, want 3", len(rec.Channels[0]))
	}
	if rec.Channels[0][0] != 0.125 {
		t.Fatalf("sample 0 = %v, want 0.125", rec.Channels[0][0])
	}
}

func TestWAVRejectsBadInput(t *testing.T) {
	if _, err := DecodeWAV(nil); !errors.Is(err, ErrWAV) {
		t.Fatalf("short err = %v", err)
	}
	junk := make([]byte, 48)
	copy(junk, "NOPE")
	if _, err := DecodeWAV(junk); !errors.Is(err, ErrWAV) {
		t.Fatalf("magic err = %v", err)
	}
	data, err := EncodeWAV([][]float64{{0}}, 8000)
	if err != nil {
		t.Fatalf("EncodeWAV returned %v", err)
	}
	nonPCM := append([]byte(nil), data...)
	binary.LittleEndian.PutUint16(nonPCM[20:22], 3)
	if _, err := DecodeWAV(nonPCM); !errors.Is(err, ErrFormat) {
		t.Fatalf("format err = %v", err)
	}
	if _, err := EncodeWAV(nil, 8000); !errors.Is(err, ErrFormat) {
		t.Fatalf("channels err = %v", err)
	}
	if _, err := EncodeWAV([][]float64{{0}}, 0); !errors.Is(err, ErrSampleRate) {
		t.Fatalf("rate err = %v", err)
	}
	if _, err := EncodeWAV([][]float64{{0}, {0, 1}}, 8000); !errors.Is(err, ErrWAV) {
		t.Fatalf("ragged err = %v", err)
	}
}
