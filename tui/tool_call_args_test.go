package tui

import (
	"encoding/json"
	"strings"
	"testing"
)

// Ports the observable contract of upstream formatToolCallWithArgs
// (.upstream/v0.99.1/packages/coding-agent/src/core/tools/render-utils.ts:71-96).
// Upstream has no direct unit test for it; tool-execution-component.test.ts:451 covers the
// component path (internal/codingagent/tool_execution_component_upstream_test.go). Each case
// below follows one branch of the function.
func TestFormatToolCallWithArgs(t *testing.T) {
	theme := ActiveTheme()
	header := theme.Fg("toolTitle", boldText("custom_tool"))
	long := strings.Repeat("x", 200)
	for _, tc := range []struct {
		name     string
		args     string
		expanded bool
		want     string
	}{
		// render-utils.ts:79-81: null and undefined arguments show the title alone.
		{"empty args", ``, false, "custom_tool"},
		{"null args", `null`, false, "custom_tool"},
		{"null args expanded", `null`, true, "custom_tool"},
		// render-utils.ts:87: an object without entries shows the title alone.
		{"empty object", `{}`, false, "custom_tool"},
		{"empty object expanded", `{}`, true, "custom_tool"},
		// render-utils.ts:83-85: a non-object value becomes the single entry "args".
		{"array", `[1,"a"]`, false, `custom_tool args=[1,"a"]`},
		{"string", `"hi"`, false, `custom_tool args="hi"`},
		{"number", `4.50`, false, `custom_tool args=4.5`},
		{"boolean", `true`, true, "custom_tool\n  args: true"},
		{"array expanded", `[1,{"a":2}]`, true, "custom_tool\n  args: [\n      1,\n      {\n        \"a\": 2\n      }\n    ]"},
		// render-utils.ts:96 collapsed: key=JSON.stringify(value) pairs joined by a space.
		{"collapsed pairs", `{"query":"pi","n":3,"ok":false,"none":null,"nested":{"a":[1,2]}}`, false, `custom_tool query="pi" n=3 ok=false none=null nested={"a":[1,2]}`},
		// JSON.stringify(value) escapes what JSON escapes; the preview keeps a lone newline as \n.
		{"collapsed escapes", `{"text":"line one\nline two \"q\" \\ tab\t"}`, false, `custom_tool text="line one\nline two \"q\" \\ tab\t"`},
		// render-utils.ts:96 preview: at most COLLAPSED_ARGS_CHARS (100) UTF-16 units, the last three replaced by "...".
		{"collapsed exactly 100", `{"k":"` + strings.Repeat("y", 100-len(`k=""`)) + `"}`, false, `custom_tool k="` + strings.Repeat("y", 100-len(`k=""`)) + `"`},
		{"collapsed 101", `{"k":"` + strings.Repeat("y", 101-len(`k=""`)) + `"}`, false, `custom_tool k="` + strings.Repeat("y", 100-len(`k=""`)-3+1) + `...`},
		{"collapsed long", `{"long":"` + long + `"}`, false, `custom_tool long="` + strings.Repeat("x", 100-len(`long="`)-3) + `...`},
		// A surrogate pair counts as two units, so the cut at unit 97 can split it: JS slice keeps the high half.
		{"collapsed astral", `{"k":"` + strings.Repeat("a", 91) + "😀😀😀" + `"}`, false, "custom_tool k=\"" + strings.Repeat("a", 91) + "😀" + "\xed\xa0\xbd" + "..."},
		// The limit counts UTF-16 units, not bytes: 30 emoji are 60 units.
		{"collapsed units not bytes", `{"k":"` + strings.Repeat("😀", 30) + `"}`, false, `custom_tool k="` + strings.Repeat("😀", 30) + `"`},
		// render-utils.ts:88-93 expanded: `  key: value`, strings raw, other values JSON.stringify(value, null, 2), continuation lines indented four spaces.
		{"expanded string raw", `{"query":"pi"}`, true, "custom_tool\n  query: pi"},
		{"expanded not truncated", `{"long":"` + long + `"}`, true, "custom_tool\n  long: " + long},
		{"expanded multiline string", `{"text":"line one\nline two\r\nline three"}`, true, "custom_tool\n  text: line one\n    line two\n    line three"},
		{"expanded tabs", `{"text":"a\tb"}`, true, "custom_tool\n  text: a   b"},
		{"expanded object", `{"opts":{"a":1,"b":[true,null]},"n":2}`, true, "custom_tool\n  opts: {\n      \"a\": 1,\n      \"b\": [\n        true,\n        null\n      ]\n    }\n  n: 2"},
		{"expanded empty containers", `{"a":{},"b":[]}`, true, "custom_tool\n  a: {}\n  b: []"},
		// Object.entries order: integer keys ascending first, then the others in insertion order; a repeated key keeps its first position and its last value.
		{"key order", `{"b":1,"2":2,"a":3,"1":4}`, false, `custom_tool 1=4 2=2 b=1 a=3`},
		{"duplicate key", `{"a":1,"b":2,"a":3}`, false, `custom_tool a=3 b=2`},
		// JSON.parse then JSON.stringify: numbers take their double value.
		{"numbers", `{"a":1.0,"b":1e21,"c":-0,"d":0.1}`, false, `custom_tool a=1 b=1e+21 c=0 d=0.1`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := FormatToolCallWithArgs("custom_tool", json.RawMessage(tc.args), theme, tc.expanded)
			plain := stripANSI(got)
			if plain != tc.want {
				t.Fatalf("FormatToolCallWithArgs(%s, expanded=%t) = %q, want %q", tc.args, tc.expanded, plain, tc.want)
			}
			if !strings.HasPrefix(got, header) {
				t.Fatalf("the title is not toolTitle + bold: %q", got)
			}
		})
	}
}

// render-utils.ts:92,96: the arguments follow the title in the muted color; collapsed on the title line after one space, expanded on their own lines.
func TestFormatToolCallWithArgsColorsTheArgumentsMuted(t *testing.T) {
	theme := ActiveTheme()
	header := theme.Fg("toolTitle", boldText("t"))
	if got, want := FormatToolCallWithArgs("t", json.RawMessage(`{"a":1}`), theme, false), header+" "+theme.Fg("muted", "a=1"); got != want {
		t.Fatalf("collapsed = %q, want %q", got, want)
	}
	if got, want := FormatToolCallWithArgs("t", json.RawMessage(`{"a":"x\ny"}`), theme, true), header+"\n"+theme.Fg("muted", "  a: x\n    y"); got != want {
		t.Fatalf("expanded = %q, want %q", got, want)
	}
	if got := FormatToolCallWithArgs("t", nil, theme, false); got != header {
		t.Fatalf("no args = %q, want %q", got, header)
	}
}

// The deprecated SetStructuredArgs keeps compiling callers and draws upstream's registered-tool fallback header.
func TestDeprecatedSetStructuredArgsDrawsTheFallbackHeader(t *testing.T) {
	c := newToolCardForTest("edit_spec", "")
	c.Label = "ignored label"
	c.SetStructuredArgs(json.RawMessage(`{"find":"alpha"}`))
	if !c.HasDefinition() {
		t.Fatal("SetStructuredArgs did not draw through the definition path")
	}
	if got := stripANSI(strings.Join(c.Render(80), "\n")); !strings.Contains(got, `edit_spec find="alpha"`) || strings.Contains(got, "ignored label") {
		t.Fatalf("card = %q", got)
	}
}
