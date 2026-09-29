package tui

// Ports packages/tui/src/terminal-colors.ts

import (
	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
)

// TerminalColorScheme is the terminal's dark or light palette preference.
type TerminalColorScheme = TerminalTheme

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
		return "light"
	}
	return "dark"
}
