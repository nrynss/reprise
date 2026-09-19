package export

import (
	"bytes"
	"fmt"
	"image/png"
)

// CoverSize reads the pixel size of PNG cover bytes without decoding
// the image. Export passes the square art through unchanged, and the
// caller asserts both sides match before bundling.
func CoverSize(cover []byte) (int, int, error) {
	if len(cover) == 0 {
		return 0, 0, fmt.Errorf("export: cover size: %w: empty panel", ErrInvalid)
	}
	config, err := png.DecodeConfig(bytes.NewReader(cover))
	if err != nil {
		return 0, 0, fmt.Errorf("export: cover size: %w", err)
	}
	return config.Width, config.Height, nil
}

// CheckCoverSquare rejects art that is not a square panel. The upload
// carries the cover at one to one, so a rectangle fails here instead of
// on the form.
func CheckCoverSquare(cover []byte) error {
	width, height, err := CoverSize(cover)
	if err != nil {
		return err
	}
	if width != height {
		return fmt.Errorf("export: cover %dx%d: %w: want a square", width, height, ErrInvalid)
	}
	return nil
}
