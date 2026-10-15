package tui

import (
	"os"
	"runtime"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/nodeurl"
	"github.com/MichaelKinsy/PiG/internal/resolvepath"
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
// URL of rawPath (resolved against cwd as upstream resolvePath does, then
// url.pathToFileURL) when the terminal supports hyperlinks; otherwise it
// returns styledText unchanged. Mirrors upstream linkPath (render-utils.ts:18).
func linkPath(styledText, rawPath, cwd string) string {
	if !GetCapabilities().Hyperlinks {
		return styledText
	}
	abs, err := resolvepath.Resolve(rawPath, cwd)
	if err != nil {
		return styledText
	}
	return Hyperlink(styledText, nodeurl.PathToFileURL(abs, runtime.GOOS == "windows"))
}

// renderToolPath styles a tool path argument: accent color, $HOME
// shortened, and (capability permitting) an OSC-8 hyperlink. An empty
// path renders as a muted "...". Mirrors upstream renderToolPath
// (render-utils.ts:75).
func renderToolPath(rawPath, cwd string) string {
	return renderToolPathArg(rawPath, true, cwd, "")
}

// renderToolPathFromArgs is renderToolPath(str(args?.file_path ?? args?.path)).
func renderToolPathFromArgs(args map[string]any, cwd string) string {
	path, ok := toolPathArg(args)
	return renderToolPathArg(path, ok, cwd, "")
}

// renderToolPathArg is renderToolPath for an argument that str() may reject:
// a non-string value (ok false) renders the invalid-arg marker, and an empty
// path renders emptyFallback, or "..." without one.
func renderToolPathArg(rawPath string, ok bool, cwd, emptyFallback string) string {
	if !ok {
		return invalidArgText()
	}
	value := rawPath
	if value == "" {
		value = emptyFallback
	}
	if value == "" {
		return fg(ActiveTheme().ToolOutput, "...")
	}
	return linkPath(fg(ActiveTheme().Accent, shortenPath(value)), value, cwd)
}

// boldText scopes bold across nested resets and line breaks, as theme.bold does through chalk.
func boldText(s string) string {
	if s == "" {
		return ""
	}
	const open = "\x1b[1m"
	s = strings.ReplaceAll(s, SGRBoldDimReset, open)
	if strings.Contains(s, "\n") {
		s = strings.NewReplacer("\r\n", SGRBoldDimReset+"\r\n"+open, "\n", SGRBoldDimReset+"\n"+open).Replace(s)
	}
	return open + s + SGRBoldDimReset
}

// toolTitleText styles a tool name with the toolTitle color and bold,
// mirroring upstream theme.fg("toolTitle", theme.bold(name)).
func toolTitleText(name string) string {
	return fg(ActiveTheme().ToolTitle, boldText(name))
}
