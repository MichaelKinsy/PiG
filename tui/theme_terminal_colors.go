package tui

// Ports the terminal-color state and light/dark detection of packages/coding-agent/src/modes/interactive/theme/theme.ts

import (
	"os"
	"slices"
	"strconv"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// The terminal's reported colors. Replaced, never mutated, on update, so themes can cache resolved colors by identity.
var terminalColorsState atomic.Pointer[TerminalColors]

// While the terminal color query is in flight, the system theme renders in grayscale.
var terminalColorsPending atomic.Bool

// The terminal's last light/dark report (mode 2031). Only used while it has not reported a background.
var terminalColorScheme atomic.Pointer[TerminalTheme]

func init() { terminalColorsState.Store(&TerminalColors{}) }

func currentTerminalColors() *TerminalColors { return terminalColorsState.Load() }

// SetTerminalColors records the terminal's reported colors. Themes use the default colors for tokens set to "" (terminal default); the system theme is generated from all of them. It ends the pending state.
func SetTerminalColors(colors TerminalColors) {
	copied := TerminalColors{Palette: slices.Clone(colors.Palette)}
	if colors.Foreground != nil {
		copied.Foreground = new(*colors.Foreground)
	}
	if colors.Background != nil {
		copied.Background = new(*colors.Background)
	}
	terminalColorsState.Store(&copied)
	terminalColorsPending.Store(false)
}

// SetTerminalColorScheme records the terminal's light/dark report, the fallback for terminals that do not report their background. An empty scheme clears it.
func SetTerminalColorScheme(scheme TerminalTheme) {
	if scheme == "" {
		terminalColorScheme.Store(nil)
		return
	}
	terminalColorScheme.Store(&scheme)
}

// MarkTerminalColorsPending renders the system theme in grayscale until SetTerminalColors reports the terminal's colors.
func MarkTerminalColorsPending() { terminalColorsPending.Store(true) }

var colorFgBgIndexPattern = lazyregexp.New(`^[0-9]{1,2}$`)

// DetectColorFgBgTheme is dark or light from the COLORFGBG variable some terminals set, or empty without a usable background index. The value is `fg;bg` or `fg;xpm;bg` (rxvt), where a field is an ANSI color index or `default` when the color is not in the palette. The index refers to the terminal's own palette, whose colors are unknown here, so it is classified by index like Vim does: 0-6 and 8 (bright black, e.g. Solarized Dark's background) are dark, 7 and 9-15 are light. A nil env is the process environment.
// upstream: packages/coding-agent/src/modes/interactive/theme/theme.ts:detectColorFgBgTheme
func DetectColorFgBgTheme(env map[string]string) TerminalTheme {
	fields := splitSemicolons(lookupColorFgBg(env))
	background := widthx.JSTrim(fields[len(fields)-1])
	if background == "" || !colorFgBgIndexPattern.MatchString(background) {
		return ""
	}
	index, _ := strconv.Atoi(background)
	if index > 15 {
		return ""
	}
	if index <= 6 || index == 8 {
		return "dark"
	}
	return "light"
}

// lookupColorFgBg reads COLORFGBG from env, or from the process environment when env is nil.
func lookupColorFgBg(env map[string]string) string {
	if env == nil {
		return os.Getenv("COLORFGBG")
	}
	return env["COLORFGBG"]
}

func splitSemicolons(value string) []string {
	var fields []string
	start := 0
	for i := 0; i < len(value); i++ {
		if value[i] == ';' {
			fields = append(fields, value[start:i])
			start = i + 1
		}
	}
	return append(fields, value[start:])
}

// DetectTerminalTheme is whether the terminal is dark or light. The background it renders decides, classified the same way the system theme does. Without a reported background: the terminal's light/dark report, then COLORFGBG, then dark. A nil env is the process environment.
// upstream: packages/coding-agent/src/modes/interactive/theme/theme.ts:detectTerminalTheme
func DetectTerminalTheme(colors TerminalColors, reportedScheme TerminalTheme, env map[string]string) TerminalTheme {
	if colors.Background != nil {
		return terminalAppearance(*colors.Background, colors.Foreground)
	}
	if reportedScheme != "" {
		return reportedScheme
	}
	if detected := DetectColorFgBgTheme(env); detected != "" {
		return detected
	}
	return "dark"
}

// GetTerminalTheme is whether the terminal is dark or light, from everything it reported so far.
func GetTerminalTheme() TerminalTheme {
	var scheme TerminalTheme
	if reported := terminalColorScheme.Load(); reported != nil {
		scheme = *reported
	}
	return DetectTerminalTheme(*currentTerminalColors(), scheme, nil)
}
