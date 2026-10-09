package tui

// Ports packages/tui/src/terminal-colors.ts

import (
	"math"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// TerminalColorScheme is the terminal's dark or light palette preference.
type TerminalColorScheme = TerminalTheme

// The two TerminalColorScheme values (terminal-colors.ts:7 `"dark" | "light"`).
const (
	TerminalColorSchemeDark  TerminalColorScheme = "dark"
	TerminalColorSchemeLight TerminalColorScheme = "light"
)

var colorSchemeReportPattern = lazyregexp.New(`^(?:\x1b\[\?997;(1|2)n)+$`)

// IsOsc11BackgroundColorResponse recognizes a complete OSC 11 reply, even when its color payload cannot be parsed.
func IsOsc11BackgroundColorResponse(data string) bool {
	return osc11BackgroundColorPattern.MatchString(data)
}

// ParseTerminalColorSchemeReport returns the last scheme in a complete sequence of palette reports, or an empty value for nonmatching input.
func ParseTerminalColorSchemeReport(data string) TerminalColorScheme {
	match := colorSchemeReportPattern.FindStringSubmatch(data)
	if match == nil {
		return ""
	}
	if match[1] == "2" {
		return TerminalColorSchemeLight
	}
	return TerminalColorSchemeDark
}

// TerminalColors are the colors the terminal reports for its current theme. A nil Foreground or Background, or a nil Palette, was not reported.
type TerminalColors struct {
	// Foreground is the default foreground (OSC 10).
	Foreground *RgbColor
	// Background is the default background (OSC 11).
	Background *RgbColor
	// Palette holds ANSI colors 0-15 (OSC 4). It is set only when the terminal reported all 16.
	Palette []RgbColor
}

// OscColorTarget names what an OSC color reply reports (upstream `"foreground" | "background" | number`): OscColorTargetForeground, OscColorTargetBackground, or a palette index (OSC 4) when non-negative.
type OscColorTarget int

const (
	OscColorTargetForeground OscColorTarget = -1
	OscColorTargetBackground OscColorTarget = -2
)

// OscColorReply is a parsed OSC 10, 11, or 4 reply. RGB is nil when the reply's color is unparseable.
type OscColorReply struct {
	Target OscColorTarget
	RGB    *RgbColor
}

var oscColorResponsePattern = lazyregexp.New(`^\x1b\](?:(1[01])|4;([0-9]{1,3}));([^\x07\x1b]*)(?:\x07|\x1b\\)$`)

// ParseOscColorResponse parses an OSC 10, 11, or 4 color reply. ok is false when data is not such a reply; the reply's RGB is nil when its color is unparseable.
func ParseOscColorResponse(data string) (reply OscColorReply, ok bool) {
	match := oscColorResponsePattern.FindStringSubmatch(data)
	if match == nil {
		return OscColorReply{}, false
	}
	switch match[1] {
	case "10":
		reply.Target = OscColorTargetForeground
	case "11":
		reply.Target = OscColorTargetBackground
	default:
		index, _ := strconv.Atoi(match[2])
		reply.Target = OscColorTarget(index)
	}
	reply.RGB = parseOscColorValue(match[3])
	return reply, true
}

// parseOscColorValue reads the color of an OSC reply: `#rrggbb`, `#rrrrggggbbbb`, or `rgb:r/g/b` and `rgba:r/g/b/a` with 1-4 hex digits per channel, using JavaScript whitespace and numeric semantics. Slash-separated colors use the first three channels; later channels do not change the RGB result.
func parseOscColorValue(rawValue string) *RgbColor {
	value := widthx.JSTrim(rawValue)
	if hex, ok := strings.CutPrefix(value, "#"); ok {
		switch len(hex) {
		case 6, 12:
			channelLength := len(hex) / 3
			r, okR := parseOscHexChannel(hex[:channelLength])
			g, okG := parseOscHexChannel(hex[channelLength : 2*channelLength])
			b, okB := parseOscHexChannel(hex[2*channelLength:])
			if !okR || !okG || !okB {
				return nil
			}
			return &RgbColor{R: r, G: g, B: b}
		default:
			return nil
		}
	}

	rgbValue := value
	if stripped, ok := strings.CutPrefix(strings.ToLower(rgbValue), "rgb:"); ok {
		rgbValue = stripped
	} else if stripped, ok := strings.CutPrefix(strings.ToLower(rgbValue), "rgba:"); ok {
		rgbValue = stripped
	}
	parts := strings.Split(rgbValue, "/")
	if len(parts) < 3 {
		return nil
	}
	r, okR := parseOscHexChannel(parts[0])
	g, okG := parseOscHexChannel(parts[1])
	b, okB := parseOscHexChannel(parts[2])
	if !okR || !okG || !okB {
		return nil
	}
	return &RgbColor{R: r, G: g, B: b}
}

// IsTerminalColorReply reports whether data has the shape of a reply to QueryTerminalColors: an OSC 10, 11, or 4 color reply or a DA1 reply. Callers that must hop to the owner loop before ConsumeTerminalColorResponse use it to skip that hop for ordinary input.
func IsTerminalColorReply(data string) bool {
	if _, ok := ParseOscColorResponse(data); ok {
		return true
	}
	return deviceAttributesResponsePattern.MatchString(data)
}

var osc11BackgroundColorPattern = lazyregexp.New(`^\x1b\]11;([^\x07\x1b]*)(?:\x07|\x1b\\)$`)

func parseOscHexChannel(channel string) (float64, bool) {
	if channel == "" {
		return 0, false
	}
	for _, r := range channel {
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return 0, false
		}
	}
	value, err := strconv.ParseFloat("0x"+channel+"p0", 64)
	if err != nil && !math.IsInf(value, 1) {
		return 0, false
	}
	maxValue := math.Pow(16, float64(len(channel))) - 1
	if maxValue <= 0 {
		return 0, false
	}
	return math.Round((value / maxValue) * 255), true
}
