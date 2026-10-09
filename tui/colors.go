package tui

// Ports packages/tui/src/colors.ts

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
)

// Color is a concrete color: IndexedColor, RgbColorValue, or OklchColorValue. Every Color converts to sRGB, so color math never fails. Upstream's `Color` union is closed; the unexported method seals it.
type Color interface {
	isColor()
	// Kind is upstream's `kind` discriminator: "indexed", "rgb" or "oklch" (colors.ts:5,10,17).
	Kind() string
}

// IndexedColor is an ANSI palette index, 0-255 (upstream `IndexedColor`, kind "indexed").
type IndexedColor struct{ Index int }

// RgbColorValue is an sRGB color with channels 0-255 (upstream `RgbColorValue`, kind "rgb"). Channels may be fractional.
type RgbColorValue struct{ R, G, B float64 }

// OklchColorValue is an OKLCH color (upstream `OklchColorValue`, kind "oklch"): lightness 0-1, chroma >= 0, hue in degrees [0, 360).
type OklchColorValue struct{ L, C, H float64 }

func (IndexedColor) isColor()    {}
func (RgbColorValue) isColor()   {}
func (OklchColorValue) isColor() {}

// Kind returns "indexed".
func (IndexedColor) Kind() string { return "indexed" }

// Kind returns "rgb".
func (RgbColorValue) Kind() string { return "rgb" }

// Kind returns "oklch".
func (OklchColorValue) Kind() string { return "oklch" }

// TerminalColorMode is the color depth a terminal accepts.
type TerminalColorMode string

const (
	TerminalColorMode256       TerminalColorMode = "256color"
	TerminalColorModeTrueColor TerminalColorMode = "truecolor"
)

// ColorMixSpace selects the space MixColors interpolates in.
type ColorMixSpace string

const (
	ColorMixSpaceOklch ColorMixSpace = "oklch"
	ColorMixSpaceSrgb  ColorMixSpace = "srgb"
)

// OklchChannels are OKLCH channels: lightness 0-1, chroma, hue in degrees.
type OklchChannels struct{ L, C, H float64 }

// OkhslChannels are OKHSL channels: hue in degrees, saturation and lightness 0-1. Saturation is relative to the most the sRGB gamut allows at that hue and lightness, so every value is in gamut.
type OkhslChannels struct{ H, S, L float64 }

// TextAttributes are the non-color attributes StyleText applies.
type TextAttributes struct {
	Bold, Dim, Italic, Underline, Inverse, Strikethrough bool
}

// TextStyle is a TextAttributes plus optional foreground and background colors; a nil Color is unset.
type TextStyle struct {
	TextAttributes
	Fg, Bg Color
}

func requireFinite(value float64, name string) error {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return fmt.Errorf("%s must be finite", name)
	}
	return nil
}

// NewIndexedColor mirrors `indexedColor`; the name differs because Go shares one namespace for types and functions.
func NewIndexedColor(index int) (IndexedColor, error) {
	if index < 0 || index > 255 {
		return IndexedColor{}, fmt.Errorf("ANSI color index must be an integer from 0 to 255: %d", index)
	}
	return IndexedColor{Index: index}, nil
}

// NewRgbColor mirrors `rgbColor`.
func NewRgbColor(r, g, b float64) (RgbColorValue, error) {
	for _, channel := range []struct {
		name  string
		value float64
	}{{"r", r}, {"g", g}, {"b", b}} {
		if err := requireFinite(channel.value, channel.name); err != nil {
			return RgbColorValue{}, err
		}
		if channel.value < 0 || channel.value > 255 {
			return RgbColorValue{}, fmt.Errorf("%s must be between 0 and 255: %s", channel.name, JSNumberString(channel.value))
		}
	}
	return RgbColorValue{R: r, G: g, B: b}, nil
}

// NewOklchColor mirrors `oklchColor`.
func NewOklchColor(l, c, h float64) (OklchColorValue, error) {
	for _, channel := range []struct {
		name  string
		value float64
	}{{"l", l}, {"c", c}, {"h", h}} {
		if err := requireFinite(channel.value, channel.name); err != nil {
			return OklchColorValue{}, err
		}
	}
	if l < 0 || l > 1 {
		return OklchColorValue{}, fmt.Errorf("l must be between 0 and 1: %s", JSNumberString(l))
	}
	if c < 0 {
		return OklchColorValue{}, fmt.Errorf("c must not be negative: %s", JSNumberString(c))
	}
	return OklchColorValue{L: l, C: c, H: jsMod(jsMod(h, 360)+360, 360)}, nil
}

const (
	colorNumberPattern = `[+-]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?`
	// JavaScript `\s`. Case-insensitive keywords are spelled out because RE2's (?i) also folds U+212A into "k", which a JavaScript /i pattern does not.
	colorSpace   = `[\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]`
	colorDegrees = `(?:[dD][eE][gG])?`
)

var (
	colorHexPattern   = lazyregexp.New(`^#([0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)
	colorOklchPattern = lazyregexp.New(`^[oO][kK][lL][cC][hH]\(` + colorSpace + `*(` + colorNumberPattern + `)(%)?` + colorSpace + `+(` + colorNumberPattern + `)` + colorSpace + `+(` + colorNumberPattern + `)` + colorDegrees + colorSpace + `*\)$`)
	colorOkhslPattern = lazyregexp.New(`^[oO][kK][hH][sS][lL]\(` + colorSpace + `*(` + colorNumberPattern + `)` + colorDegrees + colorSpace + `+(` + colorNumberPattern + `)(%)?` + colorSpace + `+(` + colorNumberPattern + `)(%)?` + colorSpace + `*\)$`)
)

// NewOkhslColor mirrors `okhslColor`: an OKHSL color converted to sRGB. Saturation is relative to the sRGB gamut at the hue and lightness, so equal saturation looks equally colorful across hues and lightness. h is in degrees; s and l are 0-1.
func NewOkhslColor(h, s, l float64) (RgbColorValue, error) {
	for _, channel := range []struct {
		name  string
		value float64
	}{{"h", h}, {"s", s}, {"l", l}} {
		if err := requireFinite(channel.value, channel.name); err != nil {
			return RgbColorValue{}, err
		}
	}
	if s < 0 || s > 1 {
		return RgbColorValue{}, fmt.Errorf("s must be between 0 and 1: %s", JSNumberString(s))
	}
	if l < 0 || l > 1 {
		return RgbColorValue{}, fmt.Errorf("l must be between 0 and 1: %s", JSNumberString(l))
	}
	rgb := okhslToRgb(h, s, l)
	return NewRgbColor(rgb.R, rgb.G, rgb.B)
}

// ColorToOkhsl converts any color to OKHSL channels.
func ColorToOkhsl(color Color) OkhslChannels { return rgbToOkhsl(ColorToRgb(color)) }

// parseColorNumber is Number.parseFloat for a string the number pattern matched; an out-of-range value is an infinity, as in JavaScript.
func parseColorNumber(text string) float64 {
	value, _ := strconv.ParseFloat(text, 64)
	return value
}

// ColorValue is upstream's `string | number` parseColor argument.
type ColorValue interface {
	string | int | float64
}

// ParseColor mirrors `parseColor`. A number is an ANSI palette index and must be an integer from 0 to 255; a string is `#rgb`, `#rrggbb`, `oklch()`, or `okhsl()`.
func ParseColor[T ColorValue](value T) (Color, error) {
	switch value := any(value).(type) {
	case string:
		return parseColorString(value)
	case int:
		return parseColorIndex(float64(value))
	case float64:
		return parseColorIndex(value)
	}
	panic("unreachable")
}

func parseColorIndex(index float64) (Color, error) {
	if math.IsNaN(index) || math.IsInf(index, 0) || index != math.Trunc(index) || index < 0 || index > 255 {
		return nil, fmt.Errorf("ANSI color index must be an integer from 0 to 255: %s", JSNumberString(index))
	}
	return IndexedColor{Index: int(index)}, nil
}

func parseColorString(value string) (Color, error) {
	if hex := colorHexPattern.FindStringSubmatch(value); hex != nil {
		digits := hex[1]
		if len(digits) == 3 {
			digits = string([]byte{digits[0], digits[0], digits[1], digits[1], digits[2], digits[2]})
		}
		channel := func(text string) float64 {
			parsed, _ := strconv.ParseUint(text, 16, 8)
			return float64(parsed)
		}
		return NewRgbColor(channel(digits[0:2]), channel(digits[2:4]), channel(digits[4:6]))
	}

	if oklch := colorOklchPattern.FindStringSubmatch(value); oklch != nil {
		lightness := parseColorNumber(oklch[1])
		if oklch[2] != "" {
			lightness /= 100
		}
		return NewOklchColor(lightness, parseColorNumber(oklch[3]), parseColorNumber(oklch[4]))
	}

	if okhsl := colorOkhslPattern.FindStringSubmatch(value); okhsl != nil {
		saturation := parseColorNumber(okhsl[2])
		if okhsl[3] != "" {
			saturation /= 100
		}
		lightness := parseColorNumber(okhsl[4])
		if okhsl[5] != "" {
			lightness /= 100
		}
		return NewOkhslColor(parseColorNumber(okhsl[1]), saturation, lightness)
	}

	return nil, fmt.Errorf("Invalid color value: %s", value)
}

var basicColors = [16]RgbColor{
	{R: 0, G: 0, B: 0},
	{R: 128, G: 0, B: 0},
	{R: 0, G: 128, B: 0},
	{R: 128, G: 128, B: 0},
	{R: 0, G: 0, B: 128},
	{R: 128, G: 0, B: 128},
	{R: 0, G: 128, B: 128},
	{R: 192, G: 192, B: 192},
	{R: 128, G: 128, B: 128},
	{R: 255, G: 0, B: 0},
	{R: 0, G: 255, B: 0},
	{R: 255, G: 255, B: 0},
	{R: 0, G: 0, B: 255},
	{R: 255, G: 0, B: 255},
	{R: 0, G: 255, B: 255},
	{R: 255, G: 255, B: 255},
}

var colorCubeValues = [6]float64{0, 95, 135, 175, 215, 255}

var colorGrayValues = func() [24]float64 {
	var values [24]float64
	for i := range values {
		values[i] = float64(8 + i*10)
	}
	return values
}()

func indexedToRgb(index int) RgbColor {
	if index < 16 {
		return basicColors[index]
	}
	if index < 232 {
		cubeIndex := index - 16
		return RgbColor{R: colorCubeValues[cubeIndex/36], G: colorCubeValues[(cubeIndex%36)/6], B: colorCubeValues[cubeIndex%6]}
	}
	gray := float64(8 + (index-232)*10)
	return RgbColor{R: gray, G: gray, B: gray}
}

func isInSrgbGamut(linear vector3) bool {
	const epsilon = 1e-7
	for _, channel := range linear {
		if !(channel >= -epsilon && channel <= 1+epsilon) {
			return false
		}
	}
	return true
}

func oklchToRgb(channels OklchChannels) RgbColor {
	// Gamut mapping keeps the hue fixed, so its direction is computed once and scaled by chroma.
	radians := (channels.H * math.Pi) / 180
	cos := math.Cos(radians)
	sin := math.Sin(radians)
	atChroma := func(chroma float64) vector3 {
		return oklabToLinearSrgb(vector3{channels.L, chroma * cos, chroma * sin})
	}

	direct := atChroma(channels.C)
	if isInSrgbGamut(direct) {
		return linearSrgbToRgb(direct)
	}

	// Reduce chroma until the color fits. The achromatic color is always in gamut, so it is the
	// fallback when no bisection step fits, e.g. `oklch(100% 0.3 150)` must map to white.
	linear := atChroma(0)
	low, high := 0.0, channels.C
	for range 20 {
		chroma := (low + high) / 2
		candidate := atChroma(chroma)
		if isInSrgbGamut(candidate) {
			low = chroma
			linear = candidate
		} else {
			high = chroma
		}
	}
	return linearSrgbToRgb(linear)
}

// ColorToRgb converts any color to sRGB.
func ColorToRgb(color Color) RgbColor {
	switch color := color.(type) {
	case IndexedColor:
		return indexedToRgb(color.Index)
	case RgbColorValue:
		return RgbColor(color)
	case OklchColorValue:
		return oklchToRgb(OklchChannels(color))
	}
	return RgbColor{}
}

// ColorToOklch converts any color to OKLCH channels.
func ColorToOklch(color Color) OklchChannels {
	if oklch, ok := color.(OklchColorValue); ok {
		return OklchChannels(oklch)
	}
	lab := rgbToOklab(ColorToRgb(color))
	return OklchChannels{L: lab[0], C: math.Hypot(lab[1], lab[2]), H: jsMod((math.Atan2(lab[2], lab[1])*180)/math.Pi+360, 360)}
}

// ColorToHex formats any color as `#rrggbb`.
func ColorToHex(color Color) string {
	rgb := ColorToRgb(color)
	channel := func(value float64) string {
		text := strconv.FormatInt(int64(jsRound(value)), 16)
		if len(text) < 2 {
			text = "0" + text
		}
		return text
	}
	return "#" + channel(rgb.R) + channel(rgb.G) + channel(rgb.B)
}

// MixColors interpolates two colors; upstream defaults `space` to oklch, so pass ColorMixSpaceOklch for the default.
func MixColors(first, second Color, amount float64, space ColorMixSpace) (Color, error) {
	if err := requireFinite(amount, "amount"); err != nil {
		return nil, err
	}
	if amount < 0 || amount > 1 {
		return nil, fmt.Errorf("amount must be between 0 and 1: %s", JSNumberString(amount))
	}

	if space == ColorMixSpaceSrgb {
		a := ColorToRgb(first)
		b := ColorToRgb(second)
		return NewRgbColor(a.R+(b.R-a.R)*amount, a.G+(b.G-a.G)*amount, a.B+(b.B-a.B)*amount)
	}

	a := ColorToOklch(first)
	b := ColorToOklch(second)
	firstHue := a.H
	if a.C < 1e-7 {
		firstHue = b.H
	}
	secondHue := b.H
	if b.C < 1e-7 {
		secondHue = firstHue
	}
	hueDelta := jsMod(secondHue-firstHue+540, 360) - 180
	return NewOklchColor(a.L+(b.L-a.L)*amount, a.C+(b.C-a.C)*amount, firstHue+hueDelta*amount)
}

func findClosest(values []float64, target float64) int {
	closestIndex := 0
	closestDistance := math.Inf(1)
	for index, value := range values {
		if distance := math.Abs(target - value); distance < closestDistance {
			closestIndex = index
			closestDistance = distance
		}
	}
	return closestIndex
}

func colorDistance(first, second RgbColor) float64 {
	dr := first.R - second.R
	dg := first.G - second.G
	db := first.B - second.B
	return dr*dr*0.299 + dg*dg*0.587 + db*db*0.114
}

func rgbToAnsi256(color RgbColor) int {
	rIndex := findClosest(colorCubeValues[:], color.R)
	gIndex := findClosest(colorCubeValues[:], color.G)
	bIndex := findClosest(colorCubeValues[:], color.B)
	cubeColor := RgbColor{R: colorCubeValues[rIndex], G: colorCubeValues[gIndex], B: colorCubeValues[bIndex]}
	cubeIndex := 16 + 36*rIndex + 6*gIndex + bIndex

	gray := jsRound(0.299*color.R + 0.587*color.G + 0.114*color.B)
	grayOffset := findClosest(colorGrayValues[:], gray)
	grayValue := colorGrayValues[grayOffset]
	spread := math.Max(color.R, math.Max(color.G, color.B)) - math.Min(color.R, math.Min(color.G, color.B))
	if spread < 10 && colorDistance(color, RgbColor{R: grayValue, G: grayValue, B: grayValue}) < colorDistance(color, cubeColor) {
		return 232 + grayOffset
	}
	return cubeIndex
}

func colorAnsi(color Color, mode TerminalColorMode, background bool) string {
	layer := 38
	if background {
		layer = 48
	}
	if indexed, ok := color.(IndexedColor); ok {
		return "\x1b[" + strconv.Itoa(layer) + ";5;" + strconv.Itoa(indexed.Index) + "m"
	}

	rgb := ColorToRgb(color)
	if mode == TerminalColorModeTrueColor {
		return "\x1b[" + strconv.Itoa(layer) + ";2;" + JSNumberString(jsRound(rgb.R)) + ";" + JSNumberString(jsRound(rgb.G)) + ";" + JSNumberString(jsRound(rgb.B)) + "m"
	}
	return "\x1b[" + strconv.Itoa(layer) + ";5;" + strconv.Itoa(rgbToAnsi256(rgb)) + "m"
}

// ForegroundAnsi is the SGR sequence that selects color as the foreground.
func ForegroundAnsi(color Color, mode TerminalColorMode) string { return colorAnsi(color, mode, false) }

// BackgroundAnsi is the SGR sequence that selects color as the background.
func BackgroundAnsi(color Color, mode TerminalColorMode) string { return colorAnsi(color, mode, true) }

// StyleText wraps text in the SGR sequences for style, closing them in reverse order.
func StyleText(text string, style TextStyle, mode TerminalColorMode) string {
	var fgAnsi, bgAnsi string
	if style.Fg != nil {
		fgAnsi = ForegroundAnsi(style.Fg, mode)
	}
	if style.Bg != nil {
		bgAnsi = BackgroundAnsi(style.Bg, mode)
	}
	return StyleTextWithAnsi(text, fgAnsi, bgAnsi, style.TextAttributes)
}

// StyleTextWithAnsi is StyleText with precomputed color sequences, e.g. cached theme colors; an empty sequence is unset.
func StyleTextWithAnsi(text, fgAnsi, bgAnsi string, attributes TextAttributes) string {
	// Resets are prepended so they close in reverse order of the opening sequences.
	var prefix strings.Builder
	suffix := ""
	if fgAnsi != "" {
		prefix.WriteString(fgAnsi)
		suffix = "\x1b[39m"
	}
	if bgAnsi != "" {
		prefix.WriteString(bgAnsi)
		suffix = "\x1b[49m" + suffix
	}
	if attributes.Bold {
		prefix.WriteString("\x1b[1m")
	}
	if attributes.Dim {
		prefix.WriteString("\x1b[2m")
	}
	if attributes.Bold || attributes.Dim {
		suffix = "\x1b[22m" + suffix
	}
	if attributes.Italic {
		prefix.WriteString("\x1b[3m")
		suffix = "\x1b[23m" + suffix
	}
	if attributes.Underline {
		prefix.WriteString("\x1b[4m")
		suffix = "\x1b[24m" + suffix
	}
	if attributes.Inverse {
		prefix.WriteString("\x1b[7m")
		suffix = "\x1b[27m" + suffix
	}
	if attributes.Strikethrough {
		prefix.WriteString("\x1b[9m")
		suffix = "\x1b[29m" + suffix
	}
	return prefix.String() + text + suffix
}
