// Scripted model stands in for the image model while no key and no
// network reach it. It records the request the pass built, then answers
// a deterministic patterned panel derived from the prompt. Tests run
// the whole pass offline through it. The production wiring will bind
// the Model interface to the image endpoint once the credential route
// covers it, with no change to the pass itself.

package cover

import (
	"bytes"
	"context"
	"hash/fnv"
	"image"
	"image/color"
	"image/png"
	"sync"
)

// ScriptedModel answers one canned image and records the request it
// heard. Set Image to return fixed bytes, or leave it empty to receive
// a deterministic panel derived from the prompt. Set Err to fail the
// call the way an unreachable provider would.
type ScriptedModel struct {
	mu    sync.Mutex
	Image []byte
	Err   error
	Calls int
	Model string
	Req   ImageRequest
}

// GenerateImage records the request and answers the canned image. It
// satisfies the Model interface, so tests bind it with no key and no
// network.
func (m *ScriptedModel) GenerateImage(_ context.Context, model string, req ImageRequest) (ImageAnswer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls++
	m.Model = model
	m.Req = req
	if m.Err != nil {
		return ImageAnswer{}, m.Err
	}
	if m.Image != nil {
		return ImageAnswer{Image: append([]byte(nil), m.Image...)}, nil
	}
	return ImageAnswer{Image: scriptedImage(req.Prompt)}, nil
}

// scriptedImage draws a deterministic panel from the prompt hash. Two
// diagonal tones keep it clear of the fallback look, which shades one
// hue down the rows, so a test can tell which path stored the cover.
func scriptedImage(prompt string) []byte {
	sum := fnv.New32a()
	_, _ = sum.Write([]byte(prompt))
	seed := sum.Sum32()
	const edge = 512
	mid := uint8(seed >> 16)
	low := uint8(seed)
	img := image.NewRGBA(image.Rect(0, 0, edge, edge))
	for y := 0; y < edge; y++ {
		for x := 0; x < edge; x++ {
			base := uint8((x*7 + y*13 + int(seed)) % 256)
			if (x+y)%32 < 16 {
				base = uint8((x*3 + y*11 + int(seed>>8)) % 256)
			}
			img.Set(x, y, color.RGBA{base, mid, low, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil
	}
	return buf.Bytes()
}
