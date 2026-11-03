package codingagent

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// sameLines reports whether a and b are the one slice a CodeHighlighter returned, not two equal results.
func sameLines(a, b []string) bool {
	return len(a) > 0 && len(a) == len(b) && &a[0] == &b[0]
}

// requireRowKeepsItsHighlighting checks, on the row's own CodeHighlighter, that draw highlights through it and that
// later redraws, with the process-wide memo of tui.HighlightCode left out of the question, return the same lines.
// The highlighter holds one result, so a seed for other code is replaced exactly when the row highlights through it.
func requireRowKeepsItsHighlighting(t *testing.T, context extension.ToolRenderContext, code, lang string, draw func(expanded bool)) {
	t.Helper()
	state := builtInState(context)
	seed := state.highlighter.Highlight("var seed = 1", lang)
	draw(false)
	if sameLines(state.highlighter.Highlight("var seed = 1", lang), seed) {
		t.Fatal("the row did not highlight through its CodeHighlighter")
	}
	kept := state.highlighter.Highlight(code, lang)
	if len(kept) == 0 {
		t.Fatal("the row's highlighter holds no lines for the code it displays")
	}
	for _, expanded := range []bool{true, false, false} {
		draw(expanded)
		if got := state.highlighter.Highlight(code, lang); !sameLines(got, kept) {
			t.Fatalf("a redraw (expanded %v) replaced the row's highlighted lines: the row highlighted its code again", expanded)
		}
	}
}

// Ports the "write highlights unchanged first lines and a complete file only once" and "codemode highlights the script
// once" tests of packages/coding-agent/test/code-highlighter.test.ts. A Go row cannot count highlight.js calls, because
// the memo of tui.HighlightCode answers a repeated call, so the tests read the row's CodeHighlighter directly.
func TestCompleteWriteRowReusesItsHighlightingAcrossRedraws(t *testing.T) {
	content := writeSource(1500)
	args, err := json.Marshal(map[string]string{"path": "sample.go", "content": content})
	if err != nil {
		t.Fatal(err)
	}
	context := extension.ToolRenderContext{State: map[string]any{}, Cwd: "/work", ArgsComplete: true}
	first := map[bool]string{}
	draw := func(expanded bool) {
		context.Expanded = expanded
		rows := strings.Join(writeRenderCall(args, nil, context).Render(120), "\n")
		if first[expanded] == "" {
			first[expanded] = rows
		}
		if rows != first[expanded] {
			t.Fatalf("a redraw changed the row\n got %q\nwant %q", rows, first[expanded])
		}
	}
	requireRowKeepsItsHighlighting(t, context, normalizeWriteText(content), "go", draw)
}

func TestCodemodeRowReusesItsScriptHighlightingAcrossRedraws(t *testing.T) {
	var script strings.Builder
	for i := range 1500 {
		fmt.Fprintf(&script, "const v%d = await tools.read({ path: \"file%d.ts\" }); // note\n", i, i)
	}
	args, err := json.Marshal(map[string]string{"code": script.String()})
	if err != nil {
		t.Fatal(err)
	}
	context := extension.ToolRenderContext{State: map[string]any{}, Cwd: "/work", ArgsComplete: true, ExecutionStarted: true}
	first := map[bool]string{}
	draw := func(expanded bool) {
		context.Expanded = expanded
		rows := strings.Join(codemodeRenderCall(args, nil, context).Render(120), "\n")
		if first[expanded] == "" {
			first[expanded] = rows
		}
		if rows != first[expanded] {
			t.Fatalf("a redraw changed the row\n got %q\nwant %q", rows, first[expanded])
		}
	}
	code := replaceTabs(jsstring.TrimEnd(strings.ReplaceAll(script.String(), "\r", "")))
	requireRowKeepsItsHighlighting(t, context, code, "javascript", draw)
}

// Ports the streaming half of "write highlights unchanged first lines and a complete file only once": a 60-line file
// grows by text on its last line, and only that last line is highlighted again; the lines before it keep their
// highlighted entries (the cache the row draws from while its arguments stream). The complete file's single
// highlighting is TestCompleteWriteRowReusesItsHighlightingAcrossRedraws.
func TestWriteRowStreamingHighlightsOnlyTheChangedLastLine(t *testing.T) {
	lines := make([]string, 60)
	for i := range lines {
		lines[i] = fmt.Sprintf("const v%d = %d;", i, i)
	}
	content := strings.Join(lines, "\n")
	state := map[string]any{}
	draw := func(text string) *writeHighlightCache {
		args, err := json.Marshal(map[string]string{"path": "a.ts", "content": text})
		if err != nil {
			t.Fatal(err)
		}
		strings.Join(writeRenderCall(args, nil, extension.ToolRenderContext{State: state, Cwd: "/", IsPartial: true, Expanded: true}).Render(80), "\n")
		return state[builtInRenderStateKey].(*builtInRenderState).writeHighlight
	}
	draw(content)
	cache := draw(content + " //")
	before := cache.lines()
	cache = draw(content + " // more")
	for i := range 59 {
		if cache.highlightedLines[i].text != before[i] {
			t.Fatalf("line %d was highlighted again or changed", i)
		}
	}
	if got := cache.line(59); got == before[59] {
		t.Fatalf("the changed last line kept its old highlighting %q", got)
	}
}
