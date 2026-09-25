package standardlogin

import "image/color"

var mascotPaletteBase = map[byte]color.RGBA{
	'.': {0, 0, 0, 0},
	'O': {0x18, 0x14, 0x1E, 0xFF},
	'K': {0x18, 0x14, 0x1E, 0xFF},
	'P': {0xFF, 0xA8, 0xB7, 0xFF},
	'p': {0xFF, 0xC4, 0xCE, 0xFF},
	's': {0xE8, 0x83, 0x96, 0xFF},
	'b': {0xFF, 0x86, 0x9A, 0xFF},
	'W': {0xFF, 0xFF, 0xFF, 0xFF},
	'e': {0xFF, 0x90, 0xA4, 0xFF},
}

// Logo colors the hero "PiG." wordmark.
type Logo struct {
	// Ramp colors the letter rows from top to bottom.
	Ramp [logoRows]color.RGBA
	// Shadow is the one-pixel drop shadow below and right of every glyph.
	Shadow color.RGBA
	// Period colors the trailing period, the accent of the website wordmark.
	Period color.RGBA
}

// logoRows is the height of the hero glyphs in pixels.
const logoRows = 12

// classicLogoRamp is the original cyan-to-navy PiG wordmark gradient.
var classicLogoRamp = [logoRows]color.RGBA{
	{0x67, 0xE8, 0xF9, 0xFF},
	{0x5A, 0xDC, 0xF4, 0xFF},
	{0x4C, 0xCF, 0xEB, 0xFF},
	{0x3B, 0xC1, 0xE2, 0xFF},
	{0x2D, 0xB3, 0xD8, 0xFF},
	{0x22, 0xA5, 0xCE, 0xFF},
	{0x18, 0x97, 0xC4, 0xFF},
	{0x10, 0x89, 0xBA, 0xFF},
	{0x08, 0x7B, 0xAF, 0xFF},
	{0x03, 0x6D, 0xA3, 0xFF},
	{0x02, 0x5F, 0x96, 0xFF},
	{0x07, 0x52, 0x85, 0xFF},
}

var classicLogoShadow = color.RGBA{0x0B, 0x20, 0x33, 0xFF}

var letterP = []string{
	"11111110.",
	"111111111",
	"111...111",
	"111...111",
	"111...111",
	"111111111",
	"11111110.",
	"111......",
	"111......",
	"111......",
	"111......",
	"111......",
}

var letterI = []string{
	"111",
	"111",
	"111",
	"...",
	"111",
	"111",
	"111",
	"111",
	"111",
	"111",
	"111",
	"111",
}

var letterG = []string{
	"..11111111.",
	".1111111111",
	"1111....111",
	"111........",
	"111........",
	"111..111111",
	"111..111111",
	"111.....111",
	"111.....111",
	"1111....111",
	".1111111111",
	"..11111111.",
}

var letterPeriod = []string{
	"111",
	"111",
	"111",
}

// heroGlyph places one glyph of the "PiG." wordmark in the 32-by-14 hero
// grid. The layout mirrors the website wordmark: tight letter spacing and a
// period set close to the G on the baseline.
type heroGlyph struct {
	rows   []string
	x, y   int
	period bool
}

var heroGlyphs = []heroGlyph{
	{rows: letterP, x: 0},
	{rows: letterI, x: 11},
	{rows: letterG, x: 16},
	{rows: letterPeriod, x: 28, y: logoRows - 3, period: true},
}

var pigMascot = []string{
	"................",
	"...OOOO..OOOO...",
	"...OeeO..OeeO...",
	"..OeePPPPPPeeO..",
	".OPPPpPPPPpPPPO.",
	".OPPWWPPPPWWPPO.",
	".OPPWKPPPPKWPPO.",
	".OPbbPssssPbbPO.",
	".OPPPsKssKsPPPO.",
	".OPPPPssssPPPPO.",
	".OPPPPPPPPPPPPO.",
	"..OPPPPPPPPPPO..",
	"...OOOOOOOOOO...",
	"................",
}
