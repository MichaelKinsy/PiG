package subprocess

import (
	"image"
	"image/color"
	"strings"
)

// pig additive (D614): where the terminal has no image protocol, the terminal view client draws an image node as half-block cells: U+2580 with the upper pixel as the foreground colour and the lower pixel as the background colour. The functions in this file are pure: no I/O, no state and no terminal detection; they are safe from any goroutine, and the same input gives the same bytes on every platform.

// halfBlockDepth is the colour form of the escapes. The caller knows the terminal.
type halfBlockDepth int

const (
	// halfBlockTrueColor writes SGR 38;2;r;g;b and 48;2;r;g;b.
	halfBlockTrueColor halfBlockDepth = iota
	// halfBlock256 writes SGR 38;5;n and 48;5;n over xterm's colour cube and grey ramp.
	halfBlock256
)

const (
	// halfBlockMaxCols and halfBlockMaxRows cap one image at 80 000 cells of at most 39 bytes each.
	halfBlockMaxCols = 400 // pig additive (D614): hard bound of the half-block fallback
	halfBlockMaxRows = 200 // pig additive (D614): hard bound of the half-block fallback
	// halfBlockOversample caps the image pixels read for one output pixel along each axis at twice this number. A larger image is read at a whole-pixel stride, so the work is bounded by the cells and not by the image.
	halfBlockOversample = 16

	halfBlockGlyph = "▀"
	halfBlockReset = "\x1b[0m"
)

// halfBlockSize returns the cells that show a pxWidth x pxHeight image inside a maxCols x maxRows box. A cell is two pixels, one above the other, and about twice as tall as wide, so the pixels are square and the aspect ratio is kept. The image is never scaled up: it takes at most one column per pixel and one row per two lines of pixels. Both results are at least 1 and at most halfBlockMaxCols and halfBlockMaxRows.
func halfBlockSize(pxWidth, pxHeight, maxCols, maxRows int) (cols, rows int) {
	maxCols, maxRows = min(max(maxCols, 1), halfBlockMaxCols), min(max(maxRows, 1), halfBlockMaxRows)
	if pxWidth < 1 || pxHeight < 1 {
		return 1, 1
	}
	pw, ph := int64(pxWidth), int64(pxHeight)
	w, h := min(int64(maxCols), pw), min(2*int64(maxRows), ph)
	if w*ph <= h*pw {
		h = max((w*ph+pw/2)/pw, 1)
	} else {
		w = max((h*pw+ph/2)/ph, 1)
	}
	return int(min(w, int64(maxCols))), int(min((h+1)/2, int64(maxRows)))
}

// halfBlockRows draws img as rows strings of cols cells each. The whole image covers the grid of cols x 2*rows pixels: a pixel of the grid is the area-weighted mean of the image pixels under it, taken on the encoded (sRGB) values and blended over background. A nil background is black; one that is not opaque counts as lying over black. Each row sets its own colours and ends with the SGR reset, so the rows may be written in any order; a row holds no newline and no cursor movement, and it writes a colour only where the colour differs from the cell before. cols and rows above the caps are clamped to them; below 1 there are no rows.
func halfBlockRows(img image.Image, cols, rows int, depth halfBlockDepth, background color.Color) []string {
	cols, rows = min(cols, halfBlockMaxCols), min(rows, halfBlockMaxRows)
	if cols < 1 || rows < 1 {
		return nil
	}
	pixels := halfBlockPixels(img, cols, 2*rows, background)
	// The longest cell sets both colours: ESC [ 38;2;255;255;255 ; 48;2;255;255;255 m and the glyph.
	longest := 2 + 16 + 1 + 16 + 1 + len(halfBlockGlyph)
	if depth == halfBlock256 {
		for i, p := range pixels {
			pixels[i] = halfBlockRGB{r: halfBlock256Index(p.r, p.g, p.b)}
		}
		longest = 2 + 8 + 1 + 8 + 1 + len(halfBlockGlyph)
	}
	var b strings.Builder
	b.Grow(rows * (cols*longest + len(halfBlockReset)))
	ends := make([]int, rows)
	for y := range rows {
		upper, lower := pixels[2*y*cols:(2*y+1)*cols], pixels[(2*y+1)*cols:(2*y+2)*cols]
		var fg, bg halfBlockRGB
		for x := range cols {
			setFg, setBg := x == 0 || upper[x] != fg, x == 0 || lower[x] != bg
			if setFg || setBg {
				b.WriteString("\x1b[")
				if setFg {
					fg = upper[x]
					halfBlockWriteColour(&b, "38", fg, depth)
				}
				if setFg && setBg {
					b.WriteByte(';')
				}
				if setBg {
					bg = lower[x]
					halfBlockWriteColour(&b, "48", bg, depth)
				}
				b.WriteByte('m')
			}
			b.WriteString(halfBlockGlyph)
		}
		b.WriteString(halfBlockReset)
		ends[y] = b.Len()
	}
	all, start := b.String(), 0
	out := make([]string, rows)
	for y, end := range ends {
		out[y], start = all[start:end], end
	}
	return out
}

// halfBlockRGB is one output pixel. In the 256-colour form r holds the palette index.
type halfBlockRGB struct{ r, g, b uint8 }

func halfBlockWriteColour(b *strings.Builder, plane string, c halfBlockRGB, depth halfBlockDepth) {
	b.WriteString(plane)
	if depth == halfBlock256 {
		b.WriteString(";5;")
		halfBlockWriteNumber(b, c.r)
		return
	}
	b.WriteString(";2;")
	halfBlockWriteNumber(b, c.r)
	b.WriteByte(';')
	halfBlockWriteNumber(b, c.g)
	b.WriteByte(';')
	halfBlockWriteNumber(b, c.b)
}

func halfBlockWriteNumber(b *strings.Builder, v uint8) {
	if v >= 100 {
		b.WriteByte('0' + v/100)
	}
	if v >= 10 {
		b.WriteByte('0' + v/10%10)
	}
	b.WriteByte('0' + v%10)
}

// halfBlockPixels scales img to w x h pixels over background, line by line from the top. Along each axis an image pixel is a span as long as the output and an output pixel is a span as long as the image, so the overlap of the two is a whole number and the weighted mean is exact in integer arithmetic.
func halfBlockPixels(img image.Image, w, h int, background color.Color) []halfBlockRGB {
	out := make([]halfBlockRGB, w*h)
	var bg [3]uint64
	if background != nil {
		r, g, b, _ := background.RGBA()
		bg = [3]uint64{uint64(r), uint64(g), uint64(b)}
	}
	bounds := img.Bounds()
	if bounds.Empty() {
		for i := range out {
			out[i] = halfBlockBlend([4]uint64{}, bg, 1)
		}
		return out
	}
	stepX, stepY := max(bounds.Dx()/(w*halfBlockOversample), 1), max(bounds.Dy()/(h*halfBlockOversample), 1)
	// vw x vh is the image as it is read: every stepX-th pixel of every stepY-th line. Each is below 2*halfBlockOversample times the output, which keeps the sums far inside 64 bits.
	vw, vh := bounds.Dx()/stepX, bounds.Dy()/stepY
	weight := uint64(vw) * uint64(vh)
	fast, _ := img.(image.RGBA64Image)
	sums := make([][4]uint64, w)
	for y := range h {
		clear(sums)
		for sy := y * vh / h; sy*h < (y+1)*vh; sy++ {
			wy := min((sy+1)*h, (y+1)*vh) - max(sy*h, y*vh)
			py := bounds.Min.Y + sy*stepY + stepY/2
			for x := range w {
				sum := &sums[x]
				for sx := x * vw / w; sx*w < (x+1)*vw; sx++ {
					wxy := uint64(wy * (min((sx+1)*w, (x+1)*vw) - max(sx*w, x*vw)))
					px := bounds.Min.X + sx*stepX + stepX/2
					var r, g, b, a uint32
					if fast != nil {
						c := fast.RGBA64At(px, py)
						r, g, b, a = uint32(c.R), uint32(c.G), uint32(c.B), uint32(c.A)
					} else {
						r, g, b, a = img.At(px, py).RGBA()
					}
					sum[0] += wxy * uint64(r)
					sum[1] += wxy * uint64(g)
					sum[2] += wxy * uint64(b)
					sum[3] += wxy * uint64(a)
				}
			}
		}
		for x := range w {
			out[y*w+x] = halfBlockBlend(sums[x], bg, weight)
		}
	}
	return out
}

// halfBlockBlend turns the weighted sums of one output pixel (alpha-premultiplied 16-bit red, green and blue, then alpha) into its 8-bit colour over the background. weight is the sum of the weights.
func halfBlockBlend(sum [4]uint64, bg [3]uint64, weight uint64) halfBlockRGB {
	opaque := weight * 0xffff
	// 0xffff is 255 x 257: dividing by opaque gives the 16-bit mean and by 257 more the 8-bit one; the result is rounded once, half up.
	den := opaque * 257
	var out [3]uint8
	for c := range out {
		num := sum[c]*0xffff + bg[c]*(opaque-min(sum[3], opaque))
		out[c] = uint8(min((num+den/2)/den, 255))
	}
	return halfBlockRGB{out[0], out[1], out[2]}
}

// halfBlock256Index returns the index of the xterm 256-colour palette entry nearest to the colour in RGB: an entry of the 6 x 6 x 6 cube (16 to 231; a channel is 0, 95, 135, 175, 215 or 255) or of the grey ramp (232 to 255; 8, 18 and so on up to 238), the cube on a tie. Indexes 0 to 15 are never used: the terminal's theme defines them.
func halfBlock256Index(r, g, b uint8) uint8 {
	channels := [3]int{int(r), int(g), int(b)}
	var cube, cubeDistance, total int
	for _, v := range channels {
		step := halfBlockCubeStep(v)
		level := 0
		if step > 0 {
			level = 55 + 40*step
		}
		cube = cube*6 + step
		cubeDistance += (v - level) * (v - level)
		total += v
	}
	// The grey nearest to a colour is the grey nearest to the mean of its channels: step = round((total/3 - 8) / 10).
	grey := min(max(total-9, 0)/30, 23)
	var greyDistance int
	for _, v := range channels {
		greyDistance += (v - 8 - 10*grey) * (v - 8 - 10*grey)
	}
	if greyDistance < cubeDistance {
		return uint8(232 + grey)
	}
	return uint8(16 + cube)
}

// halfBlockCubeStep returns the cube step whose level is nearest to v. The limits are the midpoints between the levels 0, 95, 135, 175, 215 and 255.
func halfBlockCubeStep(v int) int {
	for step, limit := range [5]int{48, 115, 155, 195, 235} {
		if v < limit {
			return step
		}
	}
	return 5
}
