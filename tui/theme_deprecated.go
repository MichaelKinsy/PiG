package tui

import (
	"strings"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// ColorMode is the terminal color mode a theme resolves its colors for.
//
// Deprecated: use TerminalColorMode; upstream unified the theme and terminal color modes.
type ColorMode = TerminalColorMode

const (
	// Deprecated: use TerminalColorModeTrueColor.
	ColorModeTrueColor = TerminalColorModeTrueColor
	// Deprecated: use TerminalColorMode256.
	ColorMode256 = TerminalColorMode256
)

// TerminalThemeDetection is the appearance DetectTerminalBackground derives from the environment.
//
// Deprecated: use DetectTerminalTheme or GetTerminalTheme.
type TerminalThemeDetection struct {
	Theme      TerminalTheme
	Source     string
	Detail     string
	Confidence string
}

// TerminalThemeDetectionOptions supplies the environment DetectTerminalBackground reads; a nil Env is the process environment.
//
// Deprecated: use DetectTerminalTheme or DetectColorFgBgTheme.
type TerminalThemeDetectionOptions struct {
	Env map[string]string
}

// DetectTerminalBackground classifies COLORFGBG like DetectColorFgBgTheme and falls back to a low-confidence dark theme without a usable index.
//
// Deprecated: use DetectColorFgBgTheme, DetectTerminalTheme or GetTerminalTheme.
func DetectTerminalBackground(options TerminalThemeDetectionOptions) TerminalThemeDetection {
	if theme := DetectColorFgBgTheme(options.Env); theme != "" {
		value := lookupColorFgBg(options.Env)
		index := widthx.JSTrim(value[strings.LastIndexByte(value, ';')+1:])
		return TerminalThemeDetection{Theme: theme, Source: "COLORFGBG", Detail: "background color index " + index, Confidence: "high"}
	}
	return TerminalThemeDetection{Theme: "dark", Source: "fallback", Detail: "no terminal background hint found", Confidence: "low"}
}

// DetectTheme activates the built-in theme for the terminal's appearance.
//
// Deprecated: use SetThemeSettingPresence; without a setting it selects the system theme.
func DetectTheme() { SetTheme(string(GetTerminalTheme())) }

// GetDefaultTheme is the built-in theme name for the terminal's appearance, "dark" or "light".
//
// Deprecated: the default theme is SystemThemeName; use GetTerminalTheme for the appearance.
func GetDefaultTheme() string { return string(GetTerminalTheme()) }

// GetThemeForRgbColor is the appearance of a terminal with this background, classified like the system theme.
//
// Deprecated: use DetectTerminalTheme.
func GetThemeForRgbColor(rgb RgbColor) TerminalTheme { return terminalAppearance(rgb, nil) }

// ParseOsc11BackgroundColor parses a strict OSC 11 reply, or returns nil.
//
// Deprecated: use ParseOscColorResponse.
func ParseOsc11BackgroundColor(data string) *RgbColor {
	match := osc11BackgroundColorPattern.FindStringSubmatch(data)
	if match == nil {
		return nil
	}
	return parseOscColorValue(match[1])
}
