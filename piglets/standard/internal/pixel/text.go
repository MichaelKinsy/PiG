package pixel

import (
	"image/color"
	"strings"
	"unicode/utf8"
)

// CellWidth returns the terminal cell width of r for the glyphs PiG Standard
// games print: pictographic emoji take two cells, everything else one.
func CellWidth(r rune) int {
	if r >= 0x1F000 {
		return 2
	}
	return 1
}

// FitLine pads or truncates text to exactly width cells. SGR sequences take
// no cells and are kept; the line ends with an SGR reset.
func FitLine(text string, width int) string {
	var out strings.Builder
	out.Grow(len(text) + width + len(reset))
	visible := 0
	for i := 0; i < len(text); {
		if text[i] == '\x1b' {
			end := strings.IndexByte(text[i:], 'm')
			if end < 0 {
				break
			}
			out.WriteString(text[i : i+end+1])
			i += end + 1
			continue
		}
		r, size := utf8.DecodeRuneInString(text[i:])
		w := CellWidth(r)
		if visible+w > width {
			break
		}
		out.WriteString(text[i : i+size])
		i += size
		visible += w
	}
	out.WriteString(reset)
	for ; visible < width; visible++ {
		out.WriteByte(' ')
	}
	return out.String()
}

// Glyph height and advance of the pixel font, in pixels.
const (
	GlyphHeight  = 5
	GlyphAdvance = 4
)

// font is a 3x5 pixel font. Lowercase text draws with the uppercase glyphs.
var font = map[rune][GlyphHeight]string{
	'0': {"###", "#.#", "#.#", "#.#", "###"},
	'1': {".#.", "##.", ".#.", ".#.", "###"},
	'2': {"##.", "..#", ".#.", "#..", "###"},
	'3': {"##.", "..#", ".#.", "..#", "##."},
	'4': {"#.#", "#.#", "###", "..#", "..#"},
	'5': {"###", "#..", "##.", "..#", "##."},
	'6': {".##", "#..", "###", "#.#", "###"},
	'7': {"###", "..#", ".#.", ".#.", ".#."},
	'8': {"###", "#.#", "###", "#.#", "###"},
	'9': {"###", "#.#", "###", "..#", "##."},
	'A': {".#.", "#.#", "###", "#.#", "#.#"},
	'B': {"##.", "#.#", "##.", "#.#", "##."},
	'C': {".##", "#..", "#..", "#..", ".##"},
	'D': {"##.", "#.#", "#.#", "#.#", "##."},
	'E': {"###", "#..", "##.", "#..", "###"},
	'F': {"###", "#..", "##.", "#..", "#.."},
	'G': {".##", "#..", "#.#", "#.#", ".##"},
	'H': {"#.#", "#.#", "###", "#.#", "#.#"},
	'I': {"###", ".#.", ".#.", ".#.", "###"},
	'J': {"..#", "..#", "..#", "#.#", ".#."},
	'K': {"#.#", "#.#", "##.", "#.#", "#.#"},
	'L': {"#..", "#..", "#..", "#..", "###"},
	'M': {"#.#", "###", "###", "#.#", "#.#"},
	'N': {"##.", "#.#", "#.#", "#.#", "#.#"},
	'O': {".#.", "#.#", "#.#", "#.#", ".#."},
	'P': {"##.", "#.#", "##.", "#..", "#.."},
	'Q': {".#.", "#.#", "#.#", "##.", ".##"},
	'R': {"##.", "#.#", "##.", "#.#", "#.#"},
	'S': {".##", "#..", ".#.", "..#", "##."},
	'T': {"###", ".#.", ".#.", ".#.", ".#."},
	'U': {"#.#", "#.#", "#.#", "#.#", "###"},
	'V': {"#.#", "#.#", "#.#", "#.#", ".#."},
	'W': {"#.#", "#.#", "###", "###", "#.#"},
	'X': {"#.#", "#.#", ".#.", "#.#", "#.#"},
	'Y': {"#.#", "#.#", ".#.", ".#.", ".#."},
	'Z': {"###", "..#", ".#.", "#..", "###"},
	':': {"...", ".#.", "...", ".#.", "..."},
	'/': {"..#", "..#", ".#.", "#..", "#.."},
	'-': {"...", "...", "###", "...", "..."},
	'+': {"...", ".#.", "###", ".#.", "..."},
	'!': {".#.", ".#.", ".#.", "...", ".#."},
	'.': {"...", "...", "...", "...", ".#."},
	'%': {"#.#", "..#", ".#.", "#..", "#.#"},
	'x': {"...", "#.#", ".#.", "#.#", "..."},
}

// TextWidth returns the pixel width of text drawn with DrawText.
func TextWidth(text string) int {
	n := utf8.RuneCountInString(text)
	if n == 0 {
		return 0
	}
	return n*GlyphAdvance - 1
}

// DrawText draws text with the 3x5 pixel font. A non-transparent shadow is
// drawn one pixel below and right of every lit pixel.
func (c *Canvas) DrawText(x, y int, text string, ink, shadow color.RGBA) {
	c.DrawTextScaled(x, y, text, 1, ink, shadow)
}

// DrawTextScaled draws text with every font pixel scale pixels square.
func (c *Canvas) DrawTextScaled(x, y int, text string, scale int, ink, shadow color.RGBA) {
	for _, r := range text {
		c.drawGlyph(x, y, r, scale, ink, shadow)
		x += GlyphAdvance * scale
	}
}

// DrawGlyph draws one character of the pixel font.
func (c *Canvas) DrawGlyph(x, y int, r rune, ink, shadow color.RGBA) {
	c.drawGlyph(x, y, r, 1, ink, shadow)
}

func (c *Canvas) drawGlyph(x, y int, r rune, scale int, ink, shadow color.RGBA) {
	glyph, ok := font[r]
	if !ok && r >= 'a' && r <= 'z' {
		glyph, ok = font[r-'a'+'A']
	}
	if !ok {
		return
	}
	for _, layer := range [2]struct {
		col    color.RGBA
		offset int
	}{{shadow, scale}, {ink, 0}} {
		for gy, row := range glyph {
			for gx := range len(row) {
				if row[gx] == '#' {
					c.Rect(x+gx*scale+layer.offset, y+gy*scale+layer.offset, scale, scale, layer.col)
				}
			}
		}
	}
}
