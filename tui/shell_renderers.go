package tui

// shell_renderers.go: call-side presentation for the built-in shell tools.
//
// Ports the call half of upstream packages/coding-agent/src/core/tools/
// renderers/bash.ts (createShellRenderers, formatShellCall, formatDuration):
// bash and powershell share one renderer and differ only in their prompt
// (ShellToolPrompt). The result half (preview, truncation warnings, "Took"
// footer) lives in internal/codingagent/tool_render_shell.go.

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// FormatShellHeader renders a shell tool call. Mirrors upstream
// formatShellCall: the whole `<prompt> <command>` is toolTitle-colored and
// bold, followed by a muted ` (timeout Ns)` suffix when the timeout argument
// is truthy. A non-string command renders as the error-colored invalid-arg
// marker; an empty or missing command renders as a toolOutput "...".
func FormatShellHeader(raw json.RawMessage, prompt string) string {
	var args map[string]any
	_ = json.Unmarshal(raw, &args)
	theme := ActiveTheme()
	command, valid := renderStr(args["command"])
	var commandDisplay string
	switch {
	case !valid:
		commandDisplay = invalidArgText()
	case command != "":
		commandDisplay = command
	default:
		commandDisplay = fg(theme.ToolOutput, "...")
	}
	timeoutSuffix := ""
	if timeout, present := args["timeout"]; present && jsTruthy(timeout) {
		timeoutSuffix = fg(theme.Muted, " (timeout "+jsTemplateString(timeout)+"s)")
	}
	return toolTitleText(prompt+" "+commandDisplay) + timeoutSuffix
}

// FormatBashHeader returns the styled `$ command` header for bash tool calls.
func FormatBashHeader(raw json.RawMessage) string {
	return FormatShellHeader(raw, "$")
}

// renderStr mirrors upstream render-utils str(): a string passes through,
// null/undefined become "", and any other value is invalid (null upstream).
func renderStr(v any) (string, bool) {
	switch s := v.(type) {
	case nil:
		return "", true
	case string:
		return s, true
	}
	return "", false
}

// invalidArgText mirrors upstream render-utils invalidArgText.
// Ports packages/coding-agent/src/core/tools/render-utils.ts (invalidArgText).
func invalidArgText() string {
	return fg(ActiveTheme().Error, "[invalid arg]")
}

// jsTruthy mirrors JavaScript truthiness for a JSON-decoded value.
func jsTruthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case float64:
		return x != 0 && !math.IsNaN(x)
	case string:
		return x != ""
	}
	return true
}

// jsTemplateString mirrors `${value}` interpolation for a JSON-decoded value.
func jsTemplateString(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case bool:
		return strconv.FormatBool(x)
	case float64:
		return JSNumberString(x)
	case string:
		return x
	case []any:
		parts := make([]string, len(x))
		for i, item := range x {
			if item != nil {
				parts[i] = jsTemplateString(item)
			}
		}
		return strings.Join(parts, ",")
	}
	return "[object Object]"
}

// JSNumberString mirrors JavaScript Number.prototype.toString() for finite
// values: the shortest round-trip digits, in plain notation for magnitudes in
// [1e-6, 1e21) and exponent notation (e.g. "1e-7", "1e+21") outside it.
// The integers 0 to 255, every RGB channel a color sequence prints, come from
// byteDecimals without formatting or allocating; -0 prints "0" as in JS.
func JSNumberString(v float64) string {
	if v >= 0 && v <= 255 {
		//portlint:allow numbers v is in [0, 255] here (NaN fails the range test), so int(v) is exact and the comparison tests integrality
		if n := int(v); float64(n) == v {
			return byteDecimal(n)
		}
	}
	switch {
	case math.IsNaN(v):
		return "NaN"
	case math.IsInf(v, 1):
		return "Infinity"
	case math.IsInf(v, -1):
		return "-Infinity"
	case v == 0:
		return "0"
	}
	abs := math.Abs(v)
	if abs >= 1e-6 && abs < 1e21 {
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	s := strconv.FormatFloat(v, 'e', -1, 64)
	mantissa, exp, _ := strings.Cut(s, "e")
	sign := exp[:1]
	digits := strings.TrimLeft(exp[1:], "0")
	return mantissa + "e" + sign + digits
}

// byteDecimals holds 0 to 255 as three zero-padded digits each.
const byteDecimals = "" +
	"000001002003004005006007008009010011012013014015016017018019020021022023024025026027028029030031" +
	"032033034035036037038039040041042043044045046047048049050051052053054055056057058059060061062063" +
	"064065066067068069070071072073074075076077078079080081082083084085086087088089090091092093094095" +
	"096097098099100101102103104105106107108109110111112113114115116117118119120121122123124125126127" +
	"128129130131132133134135136137138139140141142143144145146147148149150151152153154155156157158159" +
	"160161162163164165166167168169170171172173174175176177178179180181182183184185186187188189190191" +
	"192193194195196197198199200201202203204205206207208209210211212213214215216217218219220221222223" +
	"224225226227228229230231232233234235236237238239240241242243244245246247248249250251252253254255"

// byteDecimal is n, 0 to 255, in decimal without leading zeros: a slice of byteDecimals.
func byteDecimal(n int) string {
	switch {
	case n < 10:
		return byteDecimals[3*n+2 : 3*n+3]
	case n < 100:
		return byteDecimals[3*n+1 : 3*n+3]
	}
	return byteDecimals[3*n : 3*n+3]
}

// FormatToolDuration mirrors upstream renderers/bash.ts formatDuration over
// the integer milliseconds Date.now() differences produce: seconds with one
// decimal under a minute, then "Xm Ys", then "Xh Ym Zs".
func FormatToolDuration(d time.Duration) string {
	ms := d.Milliseconds()
	seconds := float64(ms) / 1000
	if seconds < 60 {
		return jsstring.ToFixed(seconds, 1) + "s"
	}
	//portlint:allow numbers seconds is a finite value of at least 60 derived from integer milliseconds, so the conversion is exact
	totalSeconds := int64(math.Floor(seconds))
	//portlint:allow numbers totalSeconds is a non-negative integer, so truncating division equals the Math.floor(totalSeconds / 60) of renderers/bash.ts formatDuration
	minutes := totalSeconds / 60
	remainder := totalSeconds % 60
	if minutes < 60 {
		return fmt.Sprintf("%dm %ds", minutes, remainder)
	}
	//portlint:allow numbers minutes is a non-negative integer, so truncating division equals Math.floor(minutes / 60)
	return fmt.Sprintf("%dh %dm %ds", minutes/60, minutes%60, remainder)
}
