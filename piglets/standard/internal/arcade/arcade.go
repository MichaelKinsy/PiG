// Package arcade is the shared look of the PiG Standard games, taken from
// the restored PiG Runner: its night palette, sky gradient, hill profiles,
// turf, two-line text HUD, and pixel-font title cards. Every game draws its
// scenery, HUD, title screen, and result screens from here.
package arcade

import (
	"image/color"
	"strconv"

	"github.com/MichaelKinsy/PiG/piglets/standard/internal/pixel"
)

// The PiG Runner night palette.
var (
	SkyTop   = color.RGBA{0x16, 0x16, 0x2A, 0xFF}
	SkyMid   = color.RGBA{0x1E, 0x1C, 0x30, 0xFF}
	SkyBot   = color.RGBA{0x24, 0x20, 0x38, 0xFF}
	Star     = color.RGBA{0x9A, 0x96, 0xB8, 0xFF}
	HillFar  = color.RGBA{0x1E, 0x28, 0x30, 0xFF}
	HillNear = color.RGBA{0x1A, 0x30, 0x2E, 0xFF}
	RoadDark = color.RGBA{0x10, 0x12, 0x1A, 0xFF}
	// Accent is the road-line green used for titles and the HUD.
	Accent = color.RGBA{0x01, 0xA9, 0x82, 0xFF}
	// FarStar is a dimmer star for far sky layers.
	FarStar = pixel.Lerp(SkyMid, Star, 0.6)
)

// SkyColor is the night-sky gradient at row y of a sky h pixels tall.
func SkyColor(y, h int) color.RGBA {
	half := max(h/2, 1)
	if y < half {
		return pixel.Lerp(SkyTop, SkyMid, float64(y)/float64(half))
	}
	return pixel.Lerp(SkyMid, SkyBot, float64(y-half)/float64(half))
}

// FarHill is the height of the far hill line at scrolled column x: a
// 40-pixel sawtooth up to 4 pixels high.
func FarHill(x int) int {
	off := x % 40
	if off < 0 {
		off += 40
	}
	if off < 20 {
		return off / 5
	}
	return (40 - off) / 5
}

// NearHill is the height of the near hill line at scrolled column x: a
// 28-pixel sawtooth up to 2 pixels high.
func NearHill(x int) int {
	off := x % 28
	if off < 0 {
		off += 28
	}
	if off < 14 {
		return off / 5
	}
	return (28 - off) / 5
}

// DrawGround draws the turf at row groundY across the canvas: the near-hill
// shadow line, the green road line, and the dark floor below.
func DrawGround(c *pixel.Canvas, groundY int) {
	c.Rect(0, groundY+1, c.W, c.H-groundY-1, RoadDark)
	c.Rect(0, groundY-1, c.W, 1, HillNear)
	c.Rect(0, groundY, c.W, 1, Accent)
}

// State is the play state a HUD reports.
type State int

const (
	Playing State = iota
	Paused
	Over
)

// HUD is the two text lines under every game: the title, score, high score,
// and control hints, then a status line.
type HUD struct {
	Title       string
	Score, High int
	Hints       string
	// Status is the second line, already styled.
	Status string
	State  State
}

const (
	reset    = "\x1b[0m"
	grey     = "\x1b[90m"
	red      = "\x1b[91;1m"
	yellow   = "\x1b[93;1m"
	HUDLines = 2
)

// Dim styles an informational status or hint.
func Dim(text string) string { return grey + text + reset }

// Alert styles the crash or game-over word of a status line.
func Alert(text string) string { return red + text + reset }

// Notice styles the pause word of a status line.
func Notice(text string) string { return yellow + text + reset }

// Lines renders the HUD exactly width cells wide. The title and score use
// the accent green, downsampled to 256 colors without 24-bit support; the
// score turns red when the game is over and yellow while paused.
func (h HUD) Lines(width int, trueColor bool) []string {
	accent := FgSeq(Accent, trueColor)
	scoreColor := accent
	switch h.State {
	case Over:
		scoreColor = red
	case Paused:
		scoreColor = yellow
	}
	return []string{
		pixel.FitLine(" "+accent+h.Title+reset+"  "+scoreColor+"score "+Pad4(h.Score)+reset+"  high "+Pad4(h.High)+"  "+Dim(h.Hints), width),
		pixel.FitLine(h.Status, width),
	}
}

// FgSeq is the SGR foreground sequence for c.
func FgSeq(c color.RGBA, trueColor bool) string {
	if trueColor {
		return "\x1b[38;2;" + strconv.Itoa(int(c.R)) + ";" + strconv.Itoa(int(c.G)) + ";" + strconv.Itoa(int(c.B)) + "m"
	}
	return "\x1b[38;5;" + strconv.Itoa(int(pixel.RGBTo256(c))) + "m"
}

// Pad4 formats a score with at least four digits.
func Pad4(n int) string {
	s := strconv.Itoa(n)
	for len(s) < 4 {
		s = "0" + s
	}
	return s
}

// Card title and subtitle styling.
var (
	cardInk    = Accent
	cardShadow = RoadDark
	cardText   = color.RGBA{0xE8, 0xF4, 0xF0, 0xFF}
)

// DrawCard draws a title screen or result screen: the title in the pixel
// font at twice the size, centered, with the subtitle below it. drop in
// [0, 1] slides the card down from above as it appears. It returns false
// when the canvas is too small to hold the card.
func DrawCard(c *pixel.Canvas, title, subtitle string, drop float64) bool {
	titleW := pixel.TextWidth(title) * 2
	height := pixel.GlyphHeight*2 + 4 + pixel.GlyphHeight
	if titleW+4 > c.W || c.H < height+6 {
		return false
	}
	top := max((c.H-height)/3, 2)
	y := int(float64(top+height) * min(max(drop, 0), 1))
	y -= height
	c.DrawTextScaled((c.W-titleW)/2, y, title, 2, cardInk, cardShadow)
	if subtitle != "" && pixel.TextWidth(subtitle)+2 <= c.W {
		c.DrawText((c.W-pixel.TextWidth(subtitle))/2, y+pixel.GlyphHeight*2+4, subtitle, cardText, cardShadow)
	}
	return true
}
