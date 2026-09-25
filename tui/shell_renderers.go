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

// JSNumberString mirrors JavaScript Number.prototype.toString() for finite
// values: the shortest round-trip digits, in plain notation for magnitudes in
// [1e-6, 1e21) and exponent notation (e.g. "1e-7", "1e+21") outside it.
func JSNumberString(v float64) string {
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
