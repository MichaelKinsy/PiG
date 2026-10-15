package tui

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

type toolCallArgsCase struct {
	Title    string `json:"title"`
	Args     string `json:"args"`
	Expanded bool   `json:"expanded"`
	Theme    string `json:"theme"`
}

// TestFormatToolCallWithArgsMatchesPi runs core/tools/render-utils.ts formatToolCallWithArgs against the real dark and light themes, and PiG's FormatToolCallWithArgs,
// over 3,000 seeded argument documents given as JSON text (whitespace, repeated keys, \u escapes, integer-like keys, numbers such as 1e21, 1.50, -0 and 12345678901234567890): objects
// with string values holding tabs, carriage returns, newlines, emoji and accented text, numbers, booleans, null, nested objects and arrays, empty objects, and non-object arguments
// (arrays, strings, numbers, booleans, null). Collapsed (cut at 100 UTF-16 units, ellipsis after 97) and expanded output must be equal byte for byte, colour escapes included.
func TestFormatToolCallWithArgsMatchesPi(t *testing.T) {
	rng := rand.New(rand.NewPCG(20261127, 11))
	pick := func(xs ...string) string { return xs[rng.IntN(len(xs))] }
	quote := func(s string) string { data, _ := json.Marshal(s); return string(data) }
	str := func() string {
		switch rng.IntN(8) {
		case 0:
			return strings.Repeat("ab😀", 1+rng.IntN(40))
		case 1:
			return pick("a\tb", "line1\nline2\r\nline3", "é小\u2028x", "\x00\x1b[31mred", "", " ", "x\ry")
		case 2:
			return strings.Repeat("long text ", 1+rng.IntN(25))
		}
		return pick("hello", "src/main.go", "<a&b>", "quote\"s", "back\\slash")
	}
	var value func(depth int) string
	value = func(depth int) string {
		switch n := rng.IntN(11); {
		case n < 3:
			return quote(str())
		case n == 3:
			return pick("1", "1.50", "-0", "1e21", "12345678901234567890", "0.1", "1E-7", "100", "-5e+3")
		case n == 4:
			return pick("true", "false")
		case n == 5:
			return "null"
		case n == 6 && depth < 2:
			var items []string
			for range rng.IntN(4) {
				items = append(items, value(depth+1))
			}
			return "[" + strings.Join(items, pick(",", " , ")) + "]"
		case n == 7 && depth < 2:
			var members []string
			for range rng.IntN(4) {
				members = append(members, quote(pick("a", "b", "z", "2", "1", "é"))+pick(":", " : ")+value(depth+1))
			}
			return "{" + strings.Join(members, ",") + "}"
		}
		return quote(str())
	}
	var cases []toolCallArgsCase
	for range 3000 {
		var args string
		switch rng.IntN(8) {
		case 0:
			args = value(1)
		case 1:
			args = "{}"
		default:
			var members []string
			for range 1 + rng.IntN(4) {
				members = append(members, quote(pick("path", "command", "x", "b", "a", "10", "2", "ключ", "dup", "dup"))+pick(":", " : ")+value(0))
			}
			args = pick("{", "{ ") + strings.Join(members, pick(",", ", ")) + pick("}", " }")
		}
		cases = append(cases, toolCallArgsCase{Title: pick("read", "my_tool", "Tool Ünï", ""), Args: args, Expanded: rng.IntN(2) == 0, Theme: pick("dark", "light")})
	}

	var want []string
	pioracle.Run(t, `
const { initTheme, theme } = await load("pi-coding-agent/modes/interactive/theme/theme.js");
const { formatToolCallWithArgs } = await load("pi-coding-agent/core/tools/render-utils.js");
const out = [];
for (const c of input) {
	initTheme(c.theme);
	out.push(formatToolCallWithArgs(c.title, JSON.parse(c.args), theme, c.expanded));
}
emit(out);`, cases, &want, "FORCE_COLOR=3", "COLORTERM=truecolor")

	// Pi picks the theme's colour mode from COLORTERM, not FORCE_COLOR, so the child gets COLORTERM=truecolor to match the TrueColor capability set below; the host's own COLORTERM must not decide the comparison.
	failures := 0
	for i, c := range cases {
		SetCapabilities(TerminalCapabilities{TrueColor: true})
		SetTheme(c.Theme)
		// The cut can split a surrogate pair; Go keeps the half as WTF-8 until the terminal write, where a lone surrogate becomes U+FFFD, which is what the oracle's JSON decoding gives Pi's string.
		got := jsstring.StreamText(FormatToolCallWithArgs(c.Title, json.RawMessage(c.Args), nil, c.Expanded))
		if got != want[i] {
			failures++
			if failures <= 6 {
				t.Errorf("case %d %s\n args %s\n Go %q\n Pi %q", i, fmt.Sprint(c.Expanded), c.Args, got, want[i])
			}
		}
	}
	t.Logf("%d cases, %d failures", len(cases), failures)
}
