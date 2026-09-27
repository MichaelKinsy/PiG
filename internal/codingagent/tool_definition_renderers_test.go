package codingagent

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/tui"
)

// Upstream ToolExecutionComponent draws a registered definition's renderCall
// and renderResult: both share one state per card, each gets its last
// component, renderResult gets the streamed partial result and then the final
// one, and a result never changes the expansion. PiG used to draw every
// extension tool with its generic header.
func TestInteractiveModeDrawsToolDefinitionRenderers(t *testing.T) {
	var lastCalls []extension.Component
	definition := extension.ToolDefinition{
		Name: "renders",
		RenderCall: func(args json.RawMessage, _ extension.Theme, context extension.ToolRenderContext) extension.Component {
			lastCalls = append(lastCalls, context.LastComponent)
			context.State.(map[string]any)["topic"] = string(args)
			return tui.NewText(fmt.Sprintf("CALL %s partial=%t", args, context.IsPartial))
		},
		RenderResult: func(result extension.AgentToolResult, options extension.ToolRenderResultOptions, _ extension.Theme, context extension.ToolRenderContext) extension.Component {
			value := result.(agent.AgentToolResult)
			return tui.NewText(fmt.Sprintf("RESULT %s %v partial=%t expanded=%t topic=%s", value.Content, value.Details, options.IsPartial, options.Expanded, context.State.(map[string]any)["topic"]))
		},
	}
	m := &InteractiveMode{
		newRunner:     inproc.NewRunner([]extension.Extension{{Name: "renderers", Tools: map[string]extension.RegisteredTool{definition.Name: {Definition: definition}}}}, ""),
		chatContainer: tui.NewContainer(),
		tuiInst:       tui.NewWithOutput(io.Discard, 80, 30),
		toolByID:      make(map[string]*tui.ToolExecutionComponent),
		toolStarts:    make(map[string]time.Time),
	}
	args := json.RawMessage(`{"topic":"alpha"}`)
	m.handleAgentEvent(agent.ToolExecutionStartEvent{ToolCallID: "call-1", ToolName: definition.Name, Args: args})
	card := m.toolByID["call-1"]
	render := func() string { return stripANSITest(strings.Join(card.Render(160), "\n")) }
	if got := render(); !strings.Contains(got, `CALL {"topic":"alpha"} partial=true`) || strings.Contains(got, "RESULT") {
		t.Fatalf("started card = %q", got)
	}
	m.handleAgentEvent(agent.ToolExecutionUpdateEvent{ToolCallID: "call-1", ToolName: definition.Name, Content: "working", Details: "partial-details"})
	if got := render(); !strings.Contains(got, `RESULT working partial-details partial=true expanded=false topic={"topic":"alpha"}`) {
		t.Fatalf("partial card = %q", got)
	}
	m.handleAgentEvent(agent.ToolExecutionEndEvent{ToolCallID: "call-1", ToolName: definition.Name, Result: agent.AgentToolResult{Content: "failed", Details: "final-details", IsError: true}})
	got := render()
	if !strings.Contains(got, `CALL {"topic":"alpha"} partial=false`) || !strings.Contains(got, "RESULT failed final-details partial=false expanded=false") {
		t.Fatalf("final card = %q", got)
	}
	if lastCalls[0] != nil || lastCalls[len(lastCalls)-1] == nil {
		t.Fatalf("renderCall last components = %v, want none first and the previous one after", lastCalls)
	}
	if strings.Contains(got, "Arguments:") {
		t.Fatalf("definition card fell back to the generic details card: %q", got)
	}
}

// plainRows is rendered rows without styling, hyperlinks and the trailing
// padding, one per line.
func plainRows(rows []string) string {
	plain := make([]string, len(rows))
	for i, row := range rows {
		plain[i] = strings.TrimRight(stripANSITest(osc8Link.ReplaceAllString(row, "")), " ")
	}
	return strings.Join(plain, "\n")
}

var osc8Link = regexp.MustCompile("\x1b]8;;[^\x1b]*\x1b\\\\")

// Upstream withBuiltInRenderers gives an extension override of a built-in
// tool name the built-in renderer it does not define, and an override without
// renderers draws both built-in renderers in the definition card.
func TestBuiltInOverrideFillsMissingRenderer(t *testing.T) {
	call := func(json.RawMessage, extension.Theme, extension.ToolRenderContext) extension.Component {
		return tui.NewText("OVERRIDE CALL")
	}
	if !usesToolDefinitionRenderers("bash", extension.ToolDefinition{}) {
		t.Fatal("a built-in override without renderers kept the built-in card")
	}
	if usesToolDefinitionRenderers("custom", extension.ToolDefinition{}) {
		t.Fatal("an extension tool without renderers left the generic details card (D59)")
	}
	filled := withBuiltInRenderers("write", extension.ToolDefinition{RenderCall: call})
	if filled.RenderResult == nil {
		t.Fatal("renderResult was not filled from the built-in write renderers")
	}
	if got := plainRows(filled.RenderCall(nil, nil, extension.ToolRenderContext{}).(tui.Component).Render(40)); strings.TrimSpace(got) != "OVERRIDE CALL" {
		t.Fatalf("the override's renderCall was replaced: %q", got)
	}
	failed := agent.AgentToolResult{Content: "disk full", IsError: true}
	rows := filled.RenderResult(failed, extension.ToolRenderResultOptions{}, nil, extension.ToolRenderContext{IsError: true, State: map[string]any{}}).(tui.Component).Render(40)
	if got := plainRows(rows); got != "\ndisk full" {
		t.Fatalf("built-in write renderResult = %q", got)
	}
	if custom := withBuiltInRenderers("custom", extension.ToolDefinition{RenderCall: call}); custom.RenderResult != nil {
		t.Fatal("a non built-in tool got a built-in renderer")
	}
}

// An override of write that only renders results keeps upstream's write
// call: the path and the first ten content lines with the expand hint.
func TestBuiltInOverrideDrawsBuiltInWriteCall(t *testing.T) {
	result := func(extension.AgentToolResult, extension.ToolRenderResultOptions, extension.Theme, extension.ToolRenderContext) extension.Component {
		return tui.NewText("OVERRIDE RESULT")
	}
	definition := extension.ToolDefinition{Name: "write", RenderResult: result}
	m := &InteractiveMode{
		newRunner:     inproc.NewRunner([]extension.Extension{{Name: "override", Tools: map[string]extension.RegisteredTool{"write": {Definition: definition}}}}, ""),
		chatContainer: tui.NewContainer(),
		tuiInst:       tui.NewWithOutput(io.Discard, 80, 30),
		toolByID:      make(map[string]*tui.ToolExecutionComponent),
		toolStarts:    make(map[string]time.Time),
		opts:          InteractiveOptions{CWD: t.TempDir()},
	}
	lines := make([]string, 12)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i+1)
	}
	args, _ := json.Marshal(map[string]string{"path": "notes.txt", "content": strings.Join(lines, "\n")})
	m.handleAgentEvent(agent.ToolExecutionStartEvent{ToolCallID: "call-1", ToolName: "write", Args: args})
	card := m.toolByID["call-1"]
	m.handleAgentEvent(agent.ToolExecutionEndEvent{ToolCallID: "call-1", ToolName: "write", Result: agent.AgentToolResult{Content: "ok"}})
	got := plainRows(card.Render(80))
	for _, want := range []string{"write notes.txt", "line 10", "... (2 more lines, 12 total,", "OVERRIDE RESULT"} {
		if !strings.Contains(got, want) {
			t.Fatalf("card lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "line 11") {
		t.Fatalf("collapsed write call showed more than ten lines:\n%s", got)
	}
}

// The built-in edit renderers compute the preview diff once the arguments
// are complete and show the result's diff only when the call did not.
func TestBuiltInEditRenderersPreviewAndResultDiff(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\ntwo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	call, result := builtInToolRenderers("edit")
	args := json.RawMessage(`{"path":"a.txt","oldText":"two","newText":"TWO"}`)
	state := map[string]any{}
	invalidated := make(chan struct{}, 1)
	context := extension.ToolRenderContext{Args: args, Cwd: dir, State: state, ArgsComplete: true, Invalidate: func() { invalidated <- struct{}{} }}
	component := call(args, nil, context).(tui.Component)
	select {
	case <-invalidated:
	case <-time.After(5 * time.Second):
		t.Fatal("the edit preview never invalidated the card")
	}
	got := plainRows(component.Render(60))
	if !strings.Contains(got, "edit a.txt") || !strings.Contains(got, "-2 two") || !strings.Contains(got, "+2 TWO") {
		t.Fatalf("edit call preview = %q", got)
	}
	done := agent.AgentToolResult{Content: "ok", Details: &tools.EditToolDetails{Diff: "-2 two\n+2 TWO"}}
	if rows := result(done, extension.ToolRenderResultOptions{}, nil, context).(tui.Component).Render(60); len(rows) != 0 {
		t.Fatalf("result repeated the call's diff: %q", rows)
	}
	other := extension.ToolRenderContext{Args: args, Cwd: dir, State: map[string]any{}}
	rows := result(done, extension.ToolRenderResultOptions{}, nil, other).(tui.Component).Render(60)
	if got := plainRows(rows); !strings.Contains(got, "-2 two") {
		t.Fatalf("result without the built-in call omitted the diff: %q", got)
	}
}

// The built-in shell result shows upstream's Took footer once the command
// that the call saw start has finished.
func TestBuiltInShellRenderersShowDuration(t *testing.T) {
	call, result := builtInToolRenderers("bash")
	state := map[string]any{}
	context := extension.ToolRenderContext{State: state, ExecutionStarted: true}
	header := plainRows(call(json.RawMessage(`{"command":"ls"}`), nil, context).(tui.Component).Render(60))
	if !strings.Contains(header, "$ ls") {
		t.Fatalf("bash call = %q", header)
	}
	rows := result(agent.AgentToolResult{Content: "a\nb"}, extension.ToolRenderResultOptions{}, nil, context).(tui.Component).Render(60)
	if got := plainRows(rows); !strings.Contains(got, "a") || !strings.Contains(got, "Took ") {
		t.Fatalf("bash result = %q", got)
	}
}

// The built-in read result draws nothing while collapsed, as upstream's
// formatReadResult does.
func TestBuiltInReadResultCollapsedIsEmpty(t *testing.T) {
	_, result := builtInToolRenderers("read")
	if rows := result(agent.AgentToolResult{Content: "text"}, extension.ToolRenderResultOptions{}, nil, extension.ToolRenderContext{State: map[string]any{}}).(tui.Component).Render(60); len(rows) != 0 {
		t.Fatalf("collapsed read result = %q", rows)
	}
	rows := result(agent.AgentToolResult{Content: "text"}, extension.ToolRenderResultOptions{Expanded: true}, nil, extension.ToolRenderContext{State: map[string]any{}}).(tui.Component).Render(60)
	if got := plainRows(rows); got != "\ntext" {
		t.Fatalf("expanded read result = %q", got)
	}
}
