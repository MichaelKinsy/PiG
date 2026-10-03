package piglogin

import (
	"image/color"

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

// TextMark is the one-line "PiG." mark for a terminal that cannot draw the pixel art. "PiG" is bold in the terminal's own
// foreground, so it reads on any background and theme whatever the sprite's colors; the period takes the wordmark's
// period color, the one accent the art and the text mark share.
func TextMark(variant Variant, mode tui.TerminalColorMode) string {
	return markBold + "PiG" + tui.ForegroundAnsi(markColor(LogoFor(variant).Period), mode) + "." + markReset
}
