package tui

import (
	"fmt"
	"image/color"
	"os"
	"strings"
)

// SupportsTrueColor reports whether the terminal advertises 24-bit color via
// the COLORTERM hint (truecolor/24bit) or Windows Terminal, matching the probe
// in DetectTerminalCapabilities. Terminals without the hint (Apple Terminal.app,
// plain xterm-256color) are treated as 256-color so callers downsample instead
// of emitting 24-bit SGR the terminal ignores, which would otherwise render the
// half-block art as a single default color.
func SupportsTrueColor() bool {
	switch strings.ToLower(os.Getenv("COLORTERM")) {
	case "truecolor", "24bit":
		return true
	}
	return os.Getenv("WT_SESSION") != ""
}

// xterm256CubeLevels are the six component levels of the xterm 6x6x6 color cube.
var xterm256CubeLevels = [6]int{0, 95, 135, 175, 215, 255}

func nearestCubeIndex(v int) int {
	best, bestDist := 0, 256
	for i, level := range xterm256CubeLevels {
		d := v - level
		if d < 0 {
			d = -d
		}
		if d < bestDist {
			best, bestDist = i, d
		}
	}
	return best
}

func sqDist(r1, g1, b1, r2, g2, b2 int) int {
	dr, dg, db := r1-r2, g1-g2, b1-b2
	return dr*dr + dg*dg + db*db
}

// RGBTo256 maps a 24-bit color to the nearest xterm-256 palette index, choosing
// between the 6x6x6 color cube (16-231) and the 24-step grayscale ramp
// (232-255) by squared RGB distance, so near-gray colors use the finer gray
// ramp rather than a coarse cube step.
func RGBTo256(c color.RGBA) uint8 {
	r, g, b := int(c.R), int(c.G), int(c.B)

	ri, gi, bi := nearestCubeIndex(r), nearestCubeIndex(g), nearestCubeIndex(b)
	cr, cg, cb := xterm256CubeLevels[ri], xterm256CubeLevels[gi], xterm256CubeLevels[bi]
	cubeIdx := 16 + 36*ri + 6*gi + bi
	cubeDist := sqDist(r, g, b, cr, cg, cb)

	// Grayscale ramp: indices 232..255 are levels 8,18,...,238.
	gray := (r + g + b) / 3
	step := min(max((gray-8+5)/10, 0), 23)
	grayLevel := 8 + 10*step
	grayDist := sqDist(r, g, b, grayLevel, grayLevel, grayLevel)

	if grayDist < cubeDist {
		return uint8(232 + step)
	}
	return uint8(cubeIdx)
}

// FgSeq returns the SGR foreground sequence for c: 24-bit (38;2) when trueColor,
// else the nearest 256-color (38;5). BgSeq is the background equivalent.
func FgSeq(c color.RGBA, trueColor bool) string {
	if trueColor {
		return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", c.R, c.G, c.B)
	}
	return fmt.Sprintf("\x1b[38;5;%dm", RGBTo256(c))
}

// BgSeq returns the SGR background sequence for c (see FgSeq).
func BgSeq(c color.RGBA, trueColor bool) string {
	if trueColor {
		return fmt.Sprintf("\x1b[48;2;%d;%d;%dm", c.R, c.G, c.B)
	}
	return fmt.Sprintf("\x1b[48;5;%dm", RGBTo256(c))
}
