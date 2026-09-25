// Package pixel draws PiG Standard game scenes as terminal pixel art. A Canvas
// holds RGBA pixels; an Encoder turns every two pixel rows into one line of
// half-block cells with 24-bit color, or the nearest xterm-256 color when the
// terminal lacks 24-bit color. Canvases and encoders reuse their buffers, so a
// steady-state frame allocates only the strings of rows that changed.
package pixel

import (
	"image/color"
	"os"
	"strconv"
	"strings"
)

// Canvas is a reusable RGBA pixel buffer. Pixels outside the canvas are
// ignored, and fully transparent colors never draw.
type Canvas struct {
	W, H int
	Px   []color.RGBA
}

// Resize sets the canvas size, reusing the pixel buffer when it is large
// enough. Pixel contents are unspecified after a resize.
func (c *Canvas) Resize(w, h int) {
	w, h = max(w, 0), max(h, 0)
	if n := w * h; cap(c.Px) >= n {
		c.Px = c.Px[:n]
	} else {
		c.Px = make([]color.RGBA, n)
	}
	c.W, c.H = w, h
}

// At returns the pixel at (x, y), or transparent outside the canvas.
func (c *Canvas) At(x, y int) color.RGBA {
	if x < 0 || x >= c.W || y < 0 || y >= c.H {
		return color.RGBA{}
	}
	return c.Px[y*c.W+x]
}

// Set paints one pixel.
func (c *Canvas) Set(x, y int, col color.RGBA) {
	if x < 0 || x >= c.W || y < 0 || y >= c.H || col.A == 0 {
		return
	}
	c.Px[y*c.W+x] = col
}

// Blend mixes col over the pixel at (x, y) with alpha in [0, 255].
func (c *Canvas) Blend(x, y int, col color.RGBA, alpha uint8) {
	if x < 0 || x >= c.W || y < 0 || y >= c.H || alpha == 0 {
		return
	}
	i := y*c.W + x
	c.Px[i] = Mix(c.Px[i], col, alpha)
}

// Rect fills a rectangle.
func (c *Canvas) Rect(x, y, w, h int, col color.RGBA) {
	if col.A == 0 {
		return
	}
	x0, y0 := max(x, 0), max(y, 0)
	x1, y1 := min(x+w, c.W), min(y+h, c.H)
	for yy := y0; yy < y1; yy++ {
		row := c.Px[yy*c.W : yy*c.W+c.W]
		for xx := x0; xx < x1; xx++ {
			row[xx] = col
		}
	}
}

// Blit draws a sprite whose rows index into pal. Transparent palette entries
// leave the canvas unchanged.
func (c *Canvas) Blit(x, y int, rows []string, pal *Palette) {
	for yy, row := range rows {
		for xx := range len(row) {
			c.Set(x+xx, y+yy, pal[row[xx]])
		}
	}
}

// BlitMirrored draws a sprite flipped left to right.
func (c *Canvas) BlitMirrored(x, y int, rows []string, pal *Palette) {
	for yy, row := range rows {
		last := len(row) - 1
		for xx := range len(row) {
			c.Set(x+xx, y+yy, pal[row[last-xx]])
		}
	}
}

// Palette maps sprite symbols to colors. The zero color is transparent.
type Palette [256]color.RGBA

// NewPalette builds a palette from a symbol map.
func NewPalette(symbols map[byte]color.RGBA) *Palette {
	var p Palette
	for symbol, col := range symbols {
		p[symbol] = col
	}
	return &p
}

// Lerp interpolates from a to b by t in [0, 1].
func Lerp(a, b color.RGBA, t float64) color.RGBA {
	return color.RGBA{
		R: uint8(float64(a.R) + (float64(b.R)-float64(a.R))*t),
		G: uint8(float64(a.G) + (float64(b.G)-float64(a.G))*t),
		B: uint8(float64(a.B) + (float64(b.B)-float64(a.B))*t),
		A: 0xFF,
	}
}

// Mix blends top over base with alpha in [0, 255].
func Mix(base, top color.RGBA, alpha uint8) color.RGBA {
	a := uint32(alpha)
	return color.RGBA{
		R: uint8((uint32(base.R)*(255-a) + uint32(top.R)*a) / 255),
		G: uint8((uint32(base.G)*(255-a) + uint32(top.G)*a) / 255),
		B: uint8((uint32(base.B)*(255-a) + uint32(top.B)*a) / 255),
		A: 0xFF,
	}
}

// Scale multiplies a color's channels by f, clamped to [0, 255].
func Scale(c color.RGBA, f float64) color.RGBA {
	ch := func(v uint8) uint8 { return uint8(min(max(float64(v)*f, 0), 255)) }
	return color.RGBA{R: ch(c.R), G: ch(c.G), B: ch(c.B), A: c.A}
}

// SupportsTrueColor reports whether the terminal advertises 24-bit color. It
// matches the host TUI's detection.
func SupportsTrueColor() bool {
	switch strings.ToLower(os.Getenv("COLORTERM")) {
	case "truecolor", "24bit":
		return true
	}
	return os.Getenv("WT_SESSION") != ""
}

const reset = "\x1b[0m"

// Encoder converts canvases to half-block terminal lines. It keeps the last
// string of every line and reuses it when the line's bytes are unchanged.
type Encoder struct {
	TrueColor bool
	buf       []byte
	last      []string
	c256      map[color.RGBA]uint8
}

// Encode appends one line per two canvas rows to dst and returns it. Each
// line is exactly c.W cells wide and ends with an SGR reset.
func (e *Encoder) Encode(c *Canvas, dst []string) []string {
	rows := (c.H + 1) / 2
	if len(e.last) != rows {
		e.last = make([]string, rows)
	}
	for r := range rows {
		e.buf = e.encodeRow(e.buf[:0], c, r*2)
		if string(e.buf) != e.last[r] {
			e.last[r] = string(e.buf)
		}
		dst = append(dst, e.last[r])
	}
	return dst
}

func (e *Encoder) encodeRow(b []byte, c *Canvas, yTop int) []byte {
	var lastFG, lastBG color.RGBA
	fgSet, bgSet := false, false
	for x := range c.W {
		top := c.Px[yTop*c.W+x]
		bot := color.RGBA{}
		if yTop+1 < c.H {
			bot = c.Px[(yTop+1)*c.W+x]
		}
		if !fgSet || top != lastFG {
			b = e.appendSGR(b, 38, top)
			lastFG, fgSet = top, true
		}
		if !bgSet || bot != lastBG {
			b = e.appendSGR(b, 48, bot)
			lastBG, bgSet = bot, true
		}
		b = append(b, "▀"...)
	}
	return append(b, reset...)
}

func (e *Encoder) appendSGR(b []byte, layer int, c color.RGBA) []byte {
	b = append(b, "\x1b["...)
	b = strconv.AppendInt(b, int64(layer), 10)
	if e.TrueColor {
		b = append(b, ";2;"...)
		b = strconv.AppendUint(b, uint64(c.R), 10)
		b = append(b, ';')
		b = strconv.AppendUint(b, uint64(c.G), 10)
		b = append(b, ';')
		b = strconv.AppendUint(b, uint64(c.B), 10)
		return append(b, 'm')
	}
	if e.c256 == nil {
		e.c256 = make(map[color.RGBA]uint8)
	}
	index, ok := e.c256[c]
	if !ok {
		index = RGBTo256(c)
		e.c256[c] = index
	}
	b = append(b, ";5;"...)
	b = strconv.AppendUint(b, uint64(index), 10)
	return append(b, 'm')
}

var cubeLevels = [6]int{0, 95, 135, 175, 215, 255}

// RGBTo256 maps a color to the nearest xterm-256 index, choosing between the
// 6x6x6 cube and the grayscale ramp by squared distance, like the host TUI.
func RGBTo256(c color.RGBA) uint8 {
	nearest := func(v int) int {
		best, bestDist := 0, 256
		for i, level := range cubeLevels {
			if d := max(v-level, level-v); d < bestDist {
				best, bestDist = i, d
			}
		}
		return best
	}
	sq := func(r1, g1, b1, r2, g2, b2 int) int {
		dr, dg, db := r1-r2, g1-g2, b1-b2
		return dr*dr + dg*dg + db*db
	}
	r, g, b := int(c.R), int(c.G), int(c.B)
	ri, gi, bi := nearest(r), nearest(g), nearest(b)
	cubeDist := sq(r, g, b, cubeLevels[ri], cubeLevels[gi], cubeLevels[bi])
	step := min(max(((r+g+b)/3-8+5)/10, 0), 23)
	level := 8 + 10*step
	if sq(r, g, b, level, level, level) < cubeDist {
		return uint8(232 + step)
	}
	return uint8(16 + 36*ri + 6*gi + bi)
}
