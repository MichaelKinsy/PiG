package codingagent

// pi: packages/coding-agent/src/extensions/codemode/renderer.ts

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Ports packages/coding-agent/test/codemode-renderer.test.ts (v1.1.0) with its original results and expectations.
// The details are the map form of the tool's ToolDetails, as a result read back from a session carries them.

var codemodeANSI = regexp.MustCompile("\x1b\\[[0-9;]*m")

func renderCodemodeResult(t *testing.T, result agent.AgentToolResult, isError bool) string {
	t.Helper()
	return renderCodemodeResultAt(t, result, isError, true, 200)
}

// renderCodemodeResultAt is render(result, isError, expanded, width) of codemode-renderer.test.ts (0.99.2).
func renderCodemodeResultAt(t *testing.T, result agent.AgentToolResult, isError, expanded bool, width int) string {
	t.Helper()
	context := extension.ToolRenderContext{Args: map[string]any{"code": ""}, ToolCallID: "call", Invalidate: func() {}, Cwd: "/", ExecutionStarted: true, ArgsComplete: true, Expanded: expanded, IsError: isError, OutputPad: 1}
	component := codemodeRenderResult(result, extension.ToolRenderResultOptions{Expanded: expanded}, nil, context)
	var lines []string
	for _, line := range component.(interface{ Render(int) []string }).Render(width) {
		lines = append(lines, strings.TrimRight(codemodeANSI.ReplaceAllString(line, ""), " \t"))
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func headerBlock() ai.TextContent {
	return ai.TextContent{Text: "Script completed\nWall time 0.1 seconds\nOutput:\n"}
}

func TestCodemodeRendererHidesTheScriptHeaderAndShowsTheOutput(t *testing.T) {
	got := renderCodemodeResult(t, agent.AgentToolResult{
		Content: []ai.ToolResultMessageContent{headerBlock(), ai.TextContent{Text: "hello"}},
		Details: map[string]any{"calls": []any{map[string]any{"id": "call/1", "name": "read", "args": `{"path":"a"}`, "status": "ok", "durationMs": 5.0}}},
	}, false)
	if want := "✓ read {\"path\":\"a\"} 5ms\n\nhello"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCodemodeRendererShowsTheCostOfModelCallsAndTheirTotal(t *testing.T) {
	call := func(id string, cost any) map[string]any {
		c := map[string]any{"id": id, "name": "models.classify", "args": "scorer/judge", "status": "ok", "durationMs": 5.0}
		if cost != nil {
			c["cost"] = cost
		}
		return c
	}
	got := renderCodemodeResult(t, agent.AgentToolResult{
		Content: []ai.ToolResultMessageContent{headerBlock()},
		Details: map[string]any{"calls": []any{call("call/models.classify/1", 0.000012936), call("call/models.classify/2", 0.02), call("call/models.classify/3", nil)}},
	}, false)
	want := strings.Join([]string{
		"✓ models.classify scorer/judge 5ms $0.000013",
		"✓ models.classify scorer/judge 5ms $0.02",
		"✓ models.classify scorer/judge 5ms",
		"Model calls: $0.02",
	}, "\n")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCodemodeRendererShowsResultsWithoutAHeaderSuchAsRejectedOptions(t *testing.T) {
	got := renderCodemodeResult(t, agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "The @options line must be followed by JavaScript source"}}}, true)
	if want := "The @options line must be followed by JavaScript source"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// codemode-renderer.test.ts "limits collapsed output to wrapped lines, not logical lines" (0.99.2): a single long line
// such as minified JSON must not fill the screen.
func TestCodemodeRendererLimitsCollapsedOutputToWrappedLinesNotLogicalLines(t *testing.T) {
	got := renderCodemodeResultAt(t, agent.AgentToolResult{
		Content: []ai.ToolResultMessageContent{headerBlock(), ai.TextContent{Text: strings.Repeat("x", 1000)}},
		Details: map[string]any{"calls": []any{}, "fullOutputPath": "/tmp/out.txt"},
	}, false, false, 50)
	lines := strings.Split(got, "\n")
	if len(lines) != 7 {
		t.Fatalf("got %d lines, want 7:\n%s", len(lines), got)
	}
	for i, line := range lines[:5] {
		if line != strings.Repeat("x", 50) {
			t.Errorf("lines[%d] = %q", i, line)
		}
	}
	if !regexp.MustCompile(`^\.\.\. \(15 more lines,`).MatchString(lines[5]) {
		t.Errorf("lines[5] = %q", lines[5])
	}
	if lines[6] != "Full output: /tmp/out.txt" {
		t.Errorf("lines[6] = %q", lines[6])
	}
}

// The branches of renderer.ts the upstream tests do not reach, asserted from its source: the total only for more than one
// priced call, two significant digits below a cent, the collapsed previews, the argument truncation and the full output path.

func renderCodemodeCollapsed(t *testing.T, result agent.AgentToolResult) string {
	t.Helper()
	component := codemodeRenderResult(result, extension.ToolRenderResultOptions{}, nil, extension.ToolRenderContext{})
	var lines []string
	for _, line := range component.(interface{ Render(int) []string }).Render(200) {
		lines = append(lines, strings.TrimRight(codemodeANSI.ReplaceAllString(line, ""), " \t"))
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func TestCodemodeRendererShowsNoTotalForASinglePricedCall(t *testing.T) {
	got := renderCodemodeResult(t, agent.AgentToolResult{
		Content: []ai.ToolResultMessageContent{headerBlock()},
		Details: map[string]any{"calls": []any{
			map[string]any{"name": "models.classify", "args": "a/b", "status": "ok", "cost": 0.0000012},
			map[string]any{"name": "read", "args": "", "status": "error", "error": "bad\nnews"},
		}},
	}, false)
	want := "✓ models.classify a/b $0.0000012\n✗ read\n    bad\n    news"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCodemodeRendererCollapsesCallsOutputAndArguments(t *testing.T) {
	var calls []any
	for i := range 10 {
		calls = append(calls, map[string]any{"name": "call" + string(rune('a'+i)), "args": strings.Repeat("x", 100), "status": "cancelled"})
	}
	got := renderCodemodeCollapsed(t, agent.AgentToolResult{
		Content: []ai.ToolResultMessageContent{headerBlock(), ai.TextContent{Text: "1\n2\n3\n4\n5\n6\n7"}},
		Details: map[string]any{"calls": calls, "fullOutputPath": "/tmp/full.txt"},
	})
	lines := strings.Split(got, "\n")
	if !strings.HasPrefix(lines[0], "... (2 earlier calls, ctrl+o to expand)") {
		t.Errorf("first line = %q", lines[0])
	}
	if want := "⊘ callc " + strings.Repeat("x", 77) + "..."; lines[1] != want {
		t.Errorf("first shown call = %q, want %q", lines[1], want)
	}
	tail := strings.Join(lines[len(lines)-8:], "\n")
	if !strings.Contains(tail, "5\n... (2 more lines, ctrl+o to expand)\nFull output: /tmp/full.txt") || strings.Contains(tail, "\n6\n") {
		t.Errorf("collapsed output = %q", tail)
	}
}

// A script can make many calls: the expanded card keeps every call line in order, and the collapsed card counts every earlier call.
func TestCodemodeRendererKeepsEveryCall(t *testing.T) {
	calls := make([]any, 5000)
	for i := range calls {
		calls[i] = map[string]any{"id": "call/" + strconv.Itoa(i), "name": "call" + strconv.Itoa(i), "args": "", "status": "ok"}
	}
	result := agent.AgentToolResult{Content: []ai.ToolResultMessageContent{headerBlock()}, Details: map[string]any{"calls": calls}}

	expanded := renderCodemodeResultAt(t, result, false, true, 200)
	lines := strings.Split(expanded, "\n")
	if len(lines) != len(calls) {
		t.Fatalf("expanded card has %d lines for %d calls", len(lines), len(calls))
	}
	if lines[0] != "✓ call0" || lines[len(lines)-1] != "✓ call"+strconv.Itoa(len(calls)-1) {
		t.Errorf("expanded card runs from %q to %q", lines[0], lines[len(lines)-1])
	}

	collapsed := strings.Split(renderCodemodeResultAt(t, result, false, false, 200), "\n")
	if want := "... (" + strconv.Itoa(len(calls)-codemodeCallPreviewCount) + " earlier calls,"; !strings.HasPrefix(collapsed[0], want) {
		t.Errorf("collapsed card starts %q, want it to start %q", collapsed[0], want)
	}
	if got := collapsed[len(collapsed)-1]; got != "✓ call"+strconv.Itoa(len(calls)-1) {
		t.Errorf("collapsed card ends %q", got)
	}
}
