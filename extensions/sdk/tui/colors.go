package tui

import "github.com/MichaelKinsy/PiG/extensions/sdk/tui/internal/engine"

// Color is pi-tui's Color: an [IndexedColor], an [RgbColorValue] or an [OklchColorValue].
type Color = engine.Color

// The color kinds of pi-tui's colors.ts.
type (
	IndexedColor    = engine.IndexedColor
	RgbColorValue   = engine.RgbColorValue
	OklchColorValue = engine.OklchColorValue
	// RgbColor is pi-tui's RgbColor, the channels colorToRgb returns.
	RgbColor          = engine.RgbColor
	OklchChannels     = engine.OklchChannels
	OkhslChannels     = engine.OkhslChannels
	TerminalColorMode = engine.TerminalColorMode
	ColorMixSpace     = engine.ColorMixSpace
	TextAttributes    = engine.TextAttributes
	TextStyle         = engine.TextStyle
	// ColorValue is parseColor's `string | number` argument.
	ColorValue = engine.ColorValue
)

// The terminal color modes and mix spaces.
const (
	TerminalColorModeTrueColor = engine.TerminalColorModeTrueColor
	TerminalColorMode256       = engine.TerminalColorMode256
	ColorMixSpaceOklch         = engine.ColorMixSpaceOklch
	ColorMixSpaceSrgb          = engine.ColorMixSpaceSrgb
)

// NewIndexedColor is pi-tui's indexedColor.
func NewIndexedColor(index int) (IndexedColor, error) { return engine.NewIndexedColor(index) }

// NewRgbColor is pi-tui's rgbColor.
func NewRgbColor(r, g, b float64) (RgbColorValue, error) { return engine.NewRgbColor(r, g, b) }

// NewOklchColor is pi-tui's oklchColor.
func NewOklchColor(l, c, h float64) (OklchColorValue, error) { return engine.NewOklchColor(l, c, h) }

// NewOkhslColor is pi-tui's okhslColor.
func NewOkhslColor(h, s, l float64) (RgbColorValue, error) { return engine.NewOkhslColor(h, s, l) }

// ParseColor is pi-tui's parseColor: "#rgb", "#rrggbb", "oklch(...)", "okhsl(...)" or an index from 0 to 255.
func ParseColor[T ColorValue](value T) (Color, error) { return engine.ParseColor(value) }

// ColorToRgb is pi-tui's colorToRgb.
func ColorToRgb(color Color) RgbColor { return engine.ColorToRgb(color) }

// ColorToHex is pi-tui's colorToHex.
func ColorToHex(color Color) string { return engine.ColorToHex(color) }

// ColorToOklch is pi-tui's colorToOklch.
func ColorToOklch(color Color) OklchChannels { return engine.ColorToOklch(color) }

// ColorToOkhsl is pi-tui's colorToOkhsl.
func ColorToOkhsl(color Color) OkhslChannels { return engine.ColorToOkhsl(color) }

// MixColors is pi-tui's mixColors; pass ColorMixSpaceOklch for upstream's default space.
func MixColors(first, second Color, amount float64, space ColorMixSpace) (Color, error) {
	return engine.MixColors(first, second, amount, space)
}

// ForegroundAnsi is pi-tui's foregroundAnsi.
func ForegroundAnsi(color Color, mode TerminalColorMode) string {
	return engine.ForegroundAnsi(color, mode)
}

// BackgroundAnsi is pi-tui's backgroundAnsi.
func BackgroundAnsi(color Color, mode TerminalColorMode) string {
	return engine.BackgroundAnsi(color, mode)
}

// StyleText is pi-tui's styleText.
func StyleText(text string, style TextStyle, mode TerminalColorMode) string {
	return engine.StyleText(text, style, mode)
}

// StyleTextWithAnsi is pi-tui's styleTextWithAnsi: StyleText with precomputed color sequences ("" for none).
func StyleTextWithAnsi(text, fgAnsi, bgAnsi string, attributes TextAttributes) string {
	return engine.StyleTextWithAnsi(text, fgAnsi, bgAnsi, attributes)
}
