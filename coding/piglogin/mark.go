package piglogin

import (
	"image/color"
	"math"
	"strings"

	"github.com/MichaelKinsy/PiG/tui"
)

// TextMarkWidth is the cell width of the text mark. It is as wide as Pi's logo (pi-logo.ts, 4 cells), so key hints beside
// it wrap where Pi's do.
const TextMarkWidth = 4

const (
	markReset = "\x1b[0m"
	markBold  = "\x1b[1m"
)

func markColor(value color.RGBA) tui.Color {
	// The channels are bytes, inside the 0..255 range NewRgbColor accepts.
	c, err := tui.NewRgbColor(float64(value.R), float64(value.G), float64(value.B))
	if err != nil {
		panic(err)
	}
	return c
}

// markLetterRows are the Ramp rows that color P, i and G: the mid-to-dark stops, where the classic ramp is legible on both
// light and dark backgrounds (contrast of at least 3.7 against white and black); its top cyan is too light for white.
var markLetterRows = [3]int{7, 8, 9}

// minMarkContrast is the least WCAG contrast a letter color must have against both white and black.
const minMarkContrast = 3.0

func luminance(c color.RGBA) float64 {
	lin := func(v uint8) float64 {
		x := float64(v) / 255
		if x <= 0.03928 {
			return x / 12.92
		}
		return math.Pow((x+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(c.R) + 0.7152*lin(c.G) + 0.0722*lin(c.B)
}

// legibleOnLightAndDark reports whether c reads on both a white and a black background.
func legibleOnLightAndDark(c color.RGBA) bool {
	l := luminance(c)
	return 1.05/(l+0.05) >= minMarkContrast && (l+0.05)/0.05 >= minMarkContrast
}

// markLetterColor is the color of letter n (0 for P, 1 for i, 2 for G): its Ramp stop, or the wordmark's period color when
// the ramp is too pale or too dark there (the default sprite's flat near-white ramp is made for a dark background).
func markLetterColor(logo Logo, n int) color.RGBA {
	if c := logo.Ramp[markLetterRows[n]]; legibleOnLightAndDark(c) {
		return c
	}
	return logo.Period
}

// TextMark is the one-line "PiG." mark for a terminal that cannot draw the pixel art. "PiG" is bold and each letter takes a
// mid or dark stop of the variant's wordmark ramp, the way Pi's text wordmark colors "Pi"; a stop that would not read on both
// light and dark backgrounds gives way to the period color. The period takes the wordmark's period color.
func TextMark(variant Variant, mode tui.TerminalColorMode) string {
	logo := LogoFor(variant)
	var b strings.Builder
	b.WriteString(markBold)
	for n, letter := range "PiG" {
		b.WriteString(tui.ForegroundAnsi(markColor(markLetterColor(logo, n)), mode))
		b.WriteRune(letter)
	}
	b.WriteString(tui.ForegroundAnsi(markColor(logo.Period), mode) + "." + markReset)
	return b.String()
}
