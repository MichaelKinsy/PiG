package tui

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// Port of packages/coding-agent/src/core/tools/render-utils.ts path
// helpers. Tool call headers style their path argument with an accent
// color, a $HOME-shortened display, and: on terminals that advertise
// OSC-8 support: a clickable file:// hyperlink.

// shortenPath replaces a leading $HOME with "~". Mirrors upstream
// shortenPath (render-utils.ts:10).
func shortenPath(path string) string {
	if path == "" {
		return ""
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if strings.HasPrefix(path, home) {
		return "~" + path[len(home):]
	}
	return path
}

// linkPath wraps styledText in an OSC-8 hyperlink targeting the file://
// URL of rawPath (resolved against cwd) when the terminal supports
// hyperlinks; otherwise it returns styledText unchanged. Mirrors
// upstream linkPath (render-utils.ts:18).
func linkPath(styledText, rawPath, cwd string) string {
	if !GetCapabilities().Hyperlinks {
		return styledText
	}
	abs := rawPath
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(cwd, abs)
	}
	u := url.URL{Scheme: "file", Path: abs}
	return Hyperlink(styledText, u.String())
}

// renderToolPath styles a tool path argument: accent color, $HOME
// shortened, and (capability permitting) an OSC-8 hyperlink. An empty
// path renders as a muted "...". Mirrors upstream renderToolPath
// (render-utils.ts:75).
func renderToolPath(rawPath, cwd string) string {
	if rawPath == "" {
		return fg(ActiveTheme().ToolOutput, "...")
	}
	return linkPath(fg(ActiveTheme().Accent, shortenPath(rawPath)), rawPath, cwd)
}

// boldText wraps s in SGR bold-on/bold-off without resetting color,
// mirroring upstream theme.bold().
func boldText(s string) string {
	return "\x1b[1m" + s + SGRBoldDimReset
}

// toolTitleText styles a tool name with the toolTitle color and bold,
// mirroring upstream theme.fg("toolTitle", theme.bold(name)).
func toolTitleText(name string) string {
	return fg(ActiveTheme().ToolTitle, boldText(name))
}
