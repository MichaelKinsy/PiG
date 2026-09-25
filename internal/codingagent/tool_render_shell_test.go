package codingagent

import (
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/tui"
)

// shellRows returns rendered rows padded like upstream Text.render output.
func shellRows(width int, rows ...string) []string {
	out := make([]string, len(rows))
	for i, row := range rows {
		out[i] = row + strings.Repeat(" ", max(0, width-len(stripANSITest(row))))
	}
	return out
}

func assertRows(t *testing.T, got, want []string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("rows mismatch\n got: %q\nwant: %q", got, want)
	}
}

// Mirrors upstream renderers/bash.ts rebuildBashResultRenderComponent in its
// collapsed state: the output is trimmed (leading whitespace included), the
// last BASH_PREVIEW_LINES visual lines follow an "earlier lines" hint, and a
// finished run ends with a "Took" footer formatted by formatDuration.
func TestShellBodyRendererCollapsedPreview(t *testing.T) {
	th := tui.ActiveTheme()
	out := func(s string) string { return th.ToolOutput + s + tui.SGRFgReset }
	muted := func(s string) string { return th.Muted + s + tui.SGRFgReset }
	hint := muted("... (2 earlier lines,") + " " + th.Dim + "ctrl+o" + tui.SGRFgReset + muted(" to expand") + muted(")")

	r := toolBodyRenderer("bash", agent.AgentToolResult{Content: "  l1\nl2\nl3\nl4\nl5\nl6\nl7\n"}, new(61500*time.Millisecond))
	want := append([]string{hint}, shellRows(60, out("l3"), out("l4"), out("l5"), out("l6"), out("l7"), "", muted("Took 1m 1s"))...)
	assertRows(t, r(60, false), want)

	// Expanded shows every line of the trimmed output.
	assertRows(t, r(60, true), shellRows(60,
		out("l1"), out("l2"), out("l3"), out("l4"), out("l5"), out("l6"), out("l7"), "", muted("Took 1m 1s")))
}

// A finished, truncated result drops the full-output footer the tool appended
// to its text and states it as the warning row instead. Line truncation names
// the shown/total line counts; byte truncation names the byte limit.
func TestShellBodyRendererTruncationWarnings(t *testing.T) {
	th := tui.ActiveTheme()
	out := func(s string) string { return th.ToolOutput + s + tui.SGRFgReset }
	warn := func(s string) string { return th.Warning + s + tui.SGRFgReset }
	content := "a\nb\n\n[Showing lines 4-5 of 5. Full output: /tmp/pig-bash-1.log]"

	byLines := &tools.BashDetails{
		Truncation:     &tools.TruncationResult{Truncated: true, TruncatedBy: "lines", OutputLines: 2, TotalLines: 5},
		FullOutputPath: "/tmp/pig-bash-1.log",
	}
	assertRows(t, toolBodyRenderer("bash", agent.AgentToolResult{Content: content, Details: byLines}, nil)(90, true),
		shellRows(90, out("a"), out("b"), "", warn("[Full output: /tmp/pig-bash-1.log. Truncated: showing 2 of 5 lines]")))

	byBytes := &tools.BashDetails{
		Truncation:     &tools.TruncationResult{Truncated: true, TruncatedBy: "bytes", OutputLines: 2, TotalLines: 5},
		FullOutputPath: "/tmp/pig-bash-1.log",
	}
	assertRows(t, toolBodyRenderer("bash", agent.AgentToolResult{Content: content, Details: byBytes}, nil)(90, true),
		shellRows(90, out("a"), out("b"), "", warn("[Full output: /tmp/pig-bash-1.log. Truncated: 2 lines shown (50.0KB limit)]")))

	// Persisted and extension-supplied results carry the upstream JSON shape.
	persisted := map[string]any{
		"truncation":     map[string]any{"truncated": true, "truncatedBy": "lines", "outputLines": float64(2), "totalLines": float64(5)},
		"fullOutputPath": "/tmp/pig-bash-1.log",
	}
	assertRows(t, toolBodyRenderer("powershell", agent.AgentToolResult{Content: content, Details: persisted}, nil)(90, true),
		shellRows(90, out("a"), out("b"), "", warn("[Full output: /tmp/pig-bash-1.log. Truncated: showing 2 of 5 lines]")))

	// A partial update keeps the footer text and shows no Took footer.
	assertRows(t, makeShellBodyRenderer(content, byLines, true, new(5*time.Second))(90, true),
		shellRows(90, out("a"), out("b"), out(""), out("[Showing lines 4-5 of 5. Full output: /tmp/pig-bash-1.log]"),
			"", warn("[Full output: /tmp/pig-bash-1.log. Truncated: showing 2 of 5 lines]")))
}

// A finished shell card shows Took whenever its start was recorded, even when
// the clock measured no time (upstream renderers/bash.ts tests startedAt, not
// the duration); a card rebuilt from the transcript recorded no start and
// shows none.
func TestShellBodyRendererShowsTookForARecordedZeroDuration(t *testing.T) {
	rendered := stripANSITest(strings.Join(makeShellBodyRenderer("done", nil, false, new(time.Duration(0)))(80, false), "\n"))
	if !strings.Contains(rendered, "Took 0.0s") {
		t.Fatalf("a run that recorded its start but measured 0 must show Took 0.0s:\n%s", rendered)
	}
	rendered = stripANSITest(strings.Join(makeShellBodyRenderer("done", nil, false, nil)(80, false), "\n"))
	if strings.Contains(rendered, "Took") {
		t.Fatalf("a card with no recorded start must not show Took:\n%s", rendered)
	}
}

// Streaming updates of a shell tool render through the shared shell renderer
// (upstream renderResult with isPartial), so the collapsed live preview uses
// the upstream "earlier lines" hint instead of the generic tail preview.
func TestInteractiveMode_ShellToolUpdateUsesShellRenderer(t *testing.T) {
	m := &InteractiveMode{
		chatContainer: tui.NewContainer(),
		tuiInst:       tui.NewWithOutput(io.Discard, 80, 30),
		toolByID:      make(map[string]*tui.ToolExecutionComponent),
		toolStarts:    make(map[string]time.Time),
	}
	args := json.RawMessage(`{"command":"Get-Content log.txt"}`)
	m.handleAgentEvent(agent.ToolExecutionStartEvent{ToolCallID: "ps-1", ToolName: "powershell", Args: args})
	m.handleAgentEvent(agent.ToolExecutionUpdateEvent{ToolCallID: "ps-1", ToolName: "powershell", Content: "1\n2\n3\n4\n5\n6", Args: args})
	comp := m.toolByID["ps-1"]
	if comp == nil || comp.BodyRenderer == nil {
		t.Fatal("powershell update did not install the shell body renderer")
	}
	rendered := stripANSITest(strings.Join(comp.Render(80), "\n"))
	for _, want := range []string{"PS> Get-Content log.txt", "... (1 earlier lines, ctrl+o to expand)", "Elapsed "} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("partial powershell card missing %q:\n%s", want, rendered)
		}
	}

	m.handleAgentEvent(agent.ToolExecutionEndEvent{ToolCallID: "ps-1", ToolName: "powershell", Result: agent.AgentToolResult{Content: "1\n2\n3\n4\n5\n6"}})
	rendered = stripANSITest(strings.Join(comp.Render(80), "\n"))
	if !strings.Contains(rendered, "Took ") || strings.Contains(rendered, "Elapsed ") {
		t.Fatalf("finished powershell card must show Took, not Elapsed:\n%s", rendered)
	}
}

// An extension override of a built-in tool name that supplies no renderCall
// keeps the built-in renderer (upstream withBuiltInRenderers) instead of the
// generic extension card.
func TestInteractiveMode_BuiltinOverrideWithoutRendererKeepsBuiltinCard(t *testing.T) {
	override := extension.ToolDefinition{Name: "powershell"}
	runner := inproc.NewRunner([]extension.Extension{{
		Name:  "override",
		Tools: map[string]extension.RegisteredTool{override.Name: {Definition: override}},
	}}, "")
	m := &InteractiveMode{
		newRunner:     runner,
		chatContainer: tui.NewContainer(),
		tuiInst:       tui.NewWithOutput(io.Discard, 80, 30),
		toolByID:      make(map[string]*tui.ToolExecutionComponent),
		toolStarts:    make(map[string]time.Time),
	}
	args := json.RawMessage(`{"command":"Get-Date"}`)
	m.handleAgentEvent(agent.ToolExecutionStartEvent{ToolCallID: "ov-1", ToolName: "powershell", Args: args})
	comp := m.toolByID["ov-1"]
	comp.SetExpanded(true)
	rendered := stripANSITest(strings.Join(comp.Render(80), "\n"))
	if !strings.Contains(rendered, "PS> Get-Date") || strings.Contains(rendered, "Arguments:") {
		t.Fatalf("override without renderCall lost the built-in shell card:\n%s", rendered)
	}
}

// The executed tool result must carry the original snapshot into the body
// renderer so its warning replaces the tool's textual footer.
func TestShellResultRendersExecutedTruncation(t *testing.T) {
	tool := &tools.BashTool{CWD: t.TempDir()}
	result, err := tool.Execute(t.Context(), "large", json.RawMessage(`{"command":"awk 'BEGIN{for(i=1;i<=3000;i++) print i}'"}`), nil)
	if err != nil || result.IsError {
		t.Fatalf("execute: %+v, %v", result, err)
	}
	details, ok := result.Details.(*tools.BashDetails)
	if !ok {
		t.Fatalf("details: %#v", result.Details)
	}
	t.Cleanup(func() { _ = os.Remove(details.FullOutputPath) })
	rendered := stripANSITest(strings.Join(toolBodyRenderer("bash", result, nil)(120, false), "\n"))
	if !strings.Contains(rendered, "Truncated: showing 2000 of 3000") ||
		!strings.Contains(rendered, "lines]") || strings.Contains(rendered, "[Showing lines") {
		t.Fatalf("truncated result card: %s", rendered)
	}
}
