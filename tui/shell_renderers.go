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
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/internal/jsnumber"
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

// jsToNumber mirrors JavaScript ToNumber for a JSON-decoded value; arrays and
// objects convert through their string form.
func jsToNumber(v any) float64 {
	switch x := v.(type) {
	case nil:
		return 0
	case bool:
		if x {
			return 1
		}
		return 0
	case float64:
		return x
	case string:
		return jsnumber.Parse(x)
	case []any:
		return jsnumber.Parse(jsTemplateString(x))
	}
	return math.NaN()
}

// jsAdd mirrors the JavaScript + operator for JSON-decoded values: string
// concatenation when either operand is or converts to a string, else addition.
func jsAdd(a, b any) any {
	_, aString := a.(string)
	_, bString := b.(string)
	_, aArray := a.([]any)
	_, bArray := b.([]any)
	_, aObject := a.(map[string]any)
	_, bObject := b.(map[string]any)
	if aString || bString || aArray || bArray || aObject || bObject {
		return jsTemplateString(a) + jsTemplateString(b)
	}
	return jsToNumber(a) + jsToNumber(b)
}

// JSNumberString mirrors JavaScript Number.prototype.toString() for finite
// values: the shortest round-trip digits, in plain notation for magnitudes in
// [1e-6, 1e21) and exponent notation (e.g. "1e-7", "1e+21") outside it.
// The integers 0 to 255, every RGB channel a color sequence prints, come from
// byteDecimals without formatting or allocating; -0 prints "0" as in JS.
func JSNumberString(v float64) string {
	if v >= 0 && v <= 255 {
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

// JSToFixed1 mirrors JavaScript Number.prototype.toFixed(1) for a finite,
// non-negative value: it rounds the exact binary value to one decimal, and an
// exact tie rounds up (0.25 → "0.3"), unlike Go's round-half-even "%.1f".
func JSToFixed1(v float64) string {
	return JSToFixed(v, 1)
}

// JSToFixed mirrors JavaScript Number.prototype.toFixed(digits) for values
// below 1e21: it rounds the exact binary magnitude and an exact tie rounds
// away from zero, unlike Go's round-half-even "%.*f".
func JSToFixed(v float64, digits int) string {
	switch {
	case math.IsNaN(v):
		return "NaN"
	case math.Abs(v) >= 1e21:
		return JSNumberString(v)
	}
	// A negative value keeps its sign even when it rounds to zero:
	// (-0.0001).toFixed(3) is "-0.000", while -0 is not below zero.
	sign := ""
	if v < 0 {
		sign = "-"
		v = -v
	}
	scale := new(big.Float).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(digits)), nil))
	scaled := new(big.Float).SetPrec(256).Mul(new(big.Float).SetPrec(0).SetFloat64(v), scale)
	whole, _ := scaled.Int(nil)
	frac := new(big.Float).SetPrec(256).Sub(scaled, new(big.Float).SetInt(whole))
	if frac.Cmp(big.NewFloat(0.5)) >= 0 {
		whole.Add(whole, big.NewInt(1))
	}
	text := whole.String()
	if digits == 0 {
		return sign + text
	}
	for len(text) <= digits {
		text = "0" + text
	}
	return sign + text[:len(text)-digits] + "." + text[len(text)-digits:]
}

// FormatToolDuration mirrors upstream renderers/bash.ts formatDuration over
// the integer milliseconds Date.now() differences produce: seconds with one
// decimal under a minute, then "Xm Ys", then "Xh Ym Zs".
func FormatToolDuration(d time.Duration) string {
	ms := d.Milliseconds()
	seconds := float64(ms) / 1000
	if seconds < 60 {
		return JSToFixed1(seconds) + "s"
	}
	totalSeconds := int64(math.Floor(seconds))
	minutes := totalSeconds / 60
	remainder := totalSeconds % 60
	if minutes < 60 {
		return fmt.Sprintf("%dm %ds", minutes, remainder)
	}
	return fmt.Sprintf("%dh %dm %ds", minutes/60, minutes%60, remainder)
}
