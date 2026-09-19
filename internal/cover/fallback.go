// Fallback draws the deterministic cover. The model path can fail, and
// the episode still needs art. The fallback derives everything from the
// episode number, so it needs no model, no network, and no money. The
// same number draws the same panel on every run.

package cover

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"strconv"
)

// Size is the edge length of every cover in pixels. Model art is
// accepted at other square sizes, while the fallback always draws here.
const Size = 1024

// glyphs draws decimal digits on a three by five grid. Each string is
// one row, with 1 for ink and 0 for paper.
var glyphs = map[byte][5]string{
	'0': {"111", "101", "101", "101", "111"},
	'1': {"010", "110", "010", "010", "111"},
	'2': {"111", "001", "111", "100", "111"},
	'3': {"111", "001", "111", "001", "111"},
	'4': {"101", "101", "111", "001", "001"},
	'5': {"111", "100", "111", "001", "111"},
	'6': {"111", "100", "111", "101", "111"},
	'7': {"111", "001", "010", "010", "010"},
	'8': {"111", "101", "111", "101", "111"},
	'9': {"111", "101", "111", "001", "111"},
}

// hueToRGB converts one hue wheel position to full saturation color.
// Saturation and value stay at full, so the panel reads as one hue.
func hueToRGB(hue float64) (uint8, uint8, uint8) {
	hue = hue - float64(int(hue/360))*360
	if hue < 0 {
		hue += 360
	}
	sector := int(hue / 60)
	frac := hue/60 - float64(sector)
	down := uint8((1 - frac) * 255)
	up := uint8(frac * 255)
	switch sector {
	case 0:
		return 255, up, 0
	case 1:
		return down, 255, 0
	case 2:
		return 0, 255, up
	case 3:
		return 0, down, 255
	case 4:
		return up, 0, 255
	default:
		return 255, 0, down
	}
}

// shade scales one channel toward black by the given ratio.
func shade(channel uint8, ratio float64) uint8 {
	return uint8(float64(channel) * ratio)
}

// FallbackImage draws the deterministic cover for one episode number.
// The hue comes from the number, the panel shades from the hue toward
// black down the rows, and the numeral sits centered in a contrasting
// tone. The same number returns the same bytes on every run.
func FallbackImage(episodeNumber int) []byte {
	hue := float64(((episodeNumber*47)%360 + 360) % 360)
	topR, topG, topB := hueToRGB(hue)
	img := image.NewRGBA(image.Rect(0, 0, Size, Size))
	for y := 0; y < Size; y++ {
		ratio := 1 - 0.55*float64(y)/float64(Size-1)
		fill := color.RGBA{shade(topR, ratio), shade(topG, ratio), shade(topB, ratio), 255}
		for x := 0; x < Size; x++ {
			img.Set(x, y, fill)
		}
	}
	digits := strconv.Itoa(episodeNumber)
	scale := 64
	cols := len(digits)*4 - 1
	textW := cols * scale
	textH := 5 * scale
	startX := (Size - textW) / 2
	startY := (Size - textH) / 2
	mid := 0.5 * (1 - 0.55*float64(startY+textH/2)/float64(Size-1))
	luma := (0.299*float64(topR) + 0.587*float64(topG) + 0.114*float64(topB)) * mid / 255
	var ink color.RGBA
	if luma > 0.5 {
		ink = color.RGBA{20, 20, 20, 255}
	} else {
		ink = color.RGBA{245, 245, 245, 255}
	}
	for i := 0; i < len(digits); i++ {
		rows, ok := glyphs[digits[i]]
		if !ok {
			continue
		}
		baseX := startX + i*4*scale
		for row := 0; row < 5; row++ {
			for col := 0; col < 3; col++ {
				if rows[row][col] != '1' {
					continue
				}
				for dy := 0; dy < scale; dy++ {
					for dx := 0; dx < scale; dx++ {
						img.Set(baseX+col*scale+dx, startY+row*scale+dy, ink)
					}
				}
			}
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil
	}
	return buf.Bytes()
}
