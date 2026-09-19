// Validation screens model art before it reaches the episode. The
// prompt refuses faces and lettering, and this screen enforces what a
// program can measure: decodable PNG bytes, a square frame, a sane
// size, and a panel that is not one flat color. Anything else falls
// back to the deterministic cover.

package cover

import (
	"bytes"
	"errors"
	"fmt"
	"image/png"
)

// ErrFormat reports bytes that do not decode as a PNG image, or a PNG
// outside the accepted size range.
var ErrFormat = errors.New("cover: not a usable PNG image")

// ErrNotSquare reports a decodable image whose width and height differ.
// Covers display at one to one, so anything else falls back.
var ErrNotSquare = errors.New("cover: image is not square")

// ErrBlank reports a square image with almost no color variation. A
// flat panel means the model returned nothing worth showing.
var ErrBlank = errors.New("cover: image carries almost no variation")

// maxImageBytes caps one model answer at twenty megabytes. Anything
// larger never reaches the decoder.
const maxImageBytes = 20 << 20

// minEdge floors the accepted edge at 256 pixels. Smaller art would
// smear across the gallery and the export.
const minEdge = 256

// Validate decodes one model answer and measures it. It returns the
// edge lengths on success. A blank panel, a rectangle, or undecodable
// bytes each fail with their own sentinel, and the caller falls back.
func Validate(img []byte) (int, int, error) {
	if len(img) == 0 || len(img) > maxImageBytes {
		return 0, 0, fmt.Errorf("cover: validate: %w: %d bytes", ErrFormat, len(img))
	}
	decoded, err := png.Decode(bytes.NewReader(img))
	if err != nil {
		return 0, 0, fmt.Errorf("cover: validate: %w", ErrFormat)
	}
	bounds := decoded.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()
	if width != height {
		return 0, 0, fmt.Errorf("cover: validate: %w: %dx%d", ErrNotSquare, width, height)
	}
	if width < minEdge {
		return 0, 0, fmt.Errorf("cover: validate: %w: edge %d", ErrFormat, width)
	}
	seen := make(map[[3]byte]struct{})
	step := width / 64
	if step < 1 {
		step = 1
	}
	for y := bounds.Min.Y; y < bounds.Max.Y; y += step {
		for x := bounds.Min.X; x < bounds.Max.X; x += step {
			r, g, b, _ := decoded.At(x, y).RGBA()
			key := [3]byte{byte(r >> 12), byte(g >> 12), byte(b >> 12)}
			seen[key] = struct{}{}
			if len(seen) >= 8 {
				return width, height, nil
			}
		}
	}
	return 0, 0, fmt.Errorf("cover: validate: %w", ErrBlank)
}
