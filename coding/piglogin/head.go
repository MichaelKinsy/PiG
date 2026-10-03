package piglogin

import (
	"fmt"
	"image/color"
	"strings"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// The pig head the startup header draws in Pi's logo slot is the sprite's 16-by-14 pig (MascotSpriteFor), the same pixels
// /sprite preview draws beside the wordmark: two pixel rows per line in half blocks, so HeadCells cells by HeadRows lines.
const (
	HeadWidth  = extension.LoginMascotWidth
	HeadHeight = extension.LoginMascotHeight
	HeadCells  = HeadWidth
	HeadRows   = HeadHeight / 2
)

// HeadFor returns the variant's pig head: its 16-by-14 pig.
func HeadFor(variant Variant) []string { return MascotSpriteFor(variant) }

// HeadPixel is one opaque pixel of a pig head: its column and row in the HeadWidth by HeadHeight grid, its symbol and its
// color.
type HeadPixel struct {
	X, Y   int
	Symbol byte
	Color  color.RGBA
}

// HeadPixels returns the opaque pixels of the variant's pig head in row-major order, the pixels HeadLines draws.
func HeadPixels(variant Variant) []HeadPixel {
	palette := paletteFor(variant)
	head := HeadFor(variant)
	pixels := make([]HeadPixel, 0, HeadWidth*HeadHeight)
	for y, row := range head {
		for x := range len(row) {
			if value, ok := palette[row[x]]; ok && row[x] != '.' && value.A != 0 {
				pixels = append(pixels, HeadPixel{X: x, Y: y, Symbol: row[x], Color: value})
			}
		}
	}
	return pixels
}

// HeadLines draws the variant's pig head in half blocks, HeadCells cells wide on each of HeadRows lines: the upper half
// block in the top pixel's color over the bottom pixel's, the lower half block where only the bottom pixel is opaque.
// Every cell ends in a reset, so a transparent pixel shows the terminal's background.
func HeadLines(variant Variant, mode tui.TerminalColorMode) []string {
	grid := make([][]*color.RGBA, HeadHeight)
	for y := range grid {
		grid[y] = make([]*color.RGBA, HeadWidth)
	}
	for _, pixel := range HeadPixels(variant) {
		value := pixel.Color
		grid[pixel.Y][pixel.X] = &value
	}
	return halfBlockLines(grid, mode)
}

// halfBlockLines draws a pixel grid of even height in half blocks, two pixel rows per line; a nil pixel is transparent.
func halfBlockLines(grid [][]*color.RGBA, mode tui.TerminalColorMode) []string {
	lines := make([]string, len(grid)/2)
	for line := range lines {
		var b strings.Builder
		for x := range grid[line*2] {
			top, bottom := grid[line*2][x], grid[line*2+1][x]
			switch {
			case top != nil && bottom != nil:
				b.WriteString(tui.ForegroundAnsi(markColor(*top), mode) + tui.BackgroundAnsi(markColor(*bottom), mode) + "▀" + markReset)
			case top != nil:
				b.WriteString(tui.ForegroundAnsi(markColor(*top), mode) + "▀" + markReset)
			case bottom != nil:
				b.WriteString(tui.ForegroundAnsi(markColor(*bottom), mode) + "▄" + markReset)
			default:
				b.WriteByte(' ')
			}
		}
		lines[line] = b.String()
	}
	return lines
}

// ArtWidth and ArtRows are the cells of a sprite's full art: the 32-pixel "PiG." wordmark, a 3-pixel gap and the
// 16-pixel pig, 14 pixels tall, at two pixel rows per line.
const (
	ArtWidth = extension.LoginHeroWidth + artGap + extension.LoginMascotWidth
	ArtRows  = extension.LoginHeroHeight / 2
	artGap   = 3
)

// ArtLines draws the variant's full art, the wordmark and the pig of its login definition (LoginDefinitionFor), the
// scene the native login template draws, in half blocks.
func ArtLines(variant Variant, mode tui.TerminalColorMode) []string {
	definition := LoginDefinitionFor(variant)
	palette := map[byte]color.RGBA{}
	for symbol, hex := range definition.Palette {
		var r, g, b uint8
		if _, err := fmt.Sscanf(hex, "#%02x%02x%02x", &r, &g, &b); err == nil {
			palette[symbol[0]] = color.RGBA{r, g, b, 0xFF}
		}
	}
	grid := make([][]*color.RGBA, extension.LoginHeroHeight)
	for y := range grid {
		row := definition.Hero[y] + strings.Repeat(".", artGap) + definition.Mascot[y]
		grid[y] = make([]*color.RGBA, len(row))
		for x := range len(row) {
			if value, ok := palette[row[x]]; ok && row[x] != '.' {
				grid[y][x] = &value
			}
		}
	}
	return halfBlockLines(grid, mode)
}
