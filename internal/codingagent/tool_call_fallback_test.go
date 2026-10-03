package codingagent

import (
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/tui"
)

func newFallbackTestMode(definitions ...extension.ToolDefinition) *InteractiveMode {
	tools := map[string]extension.RegisteredTool{}
	for _, definition := range definitions {
		tools[definition.Name] = extension.RegisteredTool{Definition: definition}
	}
	return &InteractiveMode{
		newRunner:     inproc.NewRunner([]extension.Extension{{Name: "test-extension", Tools: tools}}, ""),
		chatContainer: tui.NewContainer(),
		tuiInst:       tui.NewWithOutput(io.Discard, 120, 30),
		toolByID:      make(map[string]*tui.ToolExecutionComponent),
		toolStarts:    make(map[string]time.Time),
	}
}

// A tool with a registered definition always draws through the definition path, so a definition without renderCall shows
// createCallFallback: formatToolCallWithArgs(toolName, args, theme, expanded)
// (.upstream/v0.99.1/packages/coding-agent/src/modes/interactive/components/tool-execution.ts:155-157,
// updateDisplay :340-343). Replaces the 0.87.1 structured-args view, where such a card kept its own retained arguments.
func TestRegisteredToolWithoutRenderersDrawsTheFallbackHeaderThroughTheEventPath(t *testing.T) {
	m := newFallbackTestMode(extension.ToolDefinition{Name: "generic_extension"})
	args := json.RawMessage(`{"find":"PRODUCTION_PATH","note":"first\nsecond"}`)
	m.handleAgentEvent(agent.ToolExecutionStartEvent{ToolCallID: "generic-1", ToolName: "generic_extension", Args: args})
	card := m.toolByID["generic-1"]
	if card == nil {
		t.Fatal("no tool card")
	}
	if !card.HasDefinition() {
		t.Fatal("a registered tool without renderers did not draw through its definition")
	}
	card.SetExpanded(false)
	if got := plainRows(card.Render(200)); !strings.Contains(got, `generic_extension find="PRODUCTION_PATH" note="first\nsecond"`) {
		t.Fatalf("collapsed card = %q", got)
	}
	m.toggleAllTools()
	expanded := plainRows(card.Render(200))
	for _, want := range []string{"generic_extension\n", "  find: PRODUCTION_PATH", "  note: first\n     second"} {
		if !strings.Contains(expanded, want) {
			t.Fatalf("expanded card lacks %q: %q", want, expanded)
		}
	}
	if strings.Contains(expanded, "Arguments:") {
		t.Fatalf("the superseded structured-args view is still drawn: %q", expanded)
	}
}

// The card's expanded state follows the global toggle for every registered tool, including ones the transcript rebuilds
// on resume (interactive_transcript.go applyToolPresentation).
func TestRegisteredToolFallbackResultKeepsTheTenLinePreview(t *testing.T) {
	m := newFallbackTestMode(extension.ToolDefinition{Name: "generic_extension"})
	m.handleAgentEvent(agent.ToolExecutionStartEvent{ToolCallID: "generic-2", ToolName: "generic_extension", Args: json.RawMessage(`{}`)})
	card := m.toolByID["generic-2"]
	lines := make([]string, 15)
	for i := range lines {
		lines[i] = "line-" + string(rune('a'+i))
	}
	m.handleAgentEvent(agent.ToolExecutionEndEvent{ToolCallID: "generic-2", ToolName: "generic_extension", Result: agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: strings.Join(lines, "\n")}}}})
	collapsed := plainRows(card.Render(120))
	if !strings.Contains(collapsed, "line-j") || strings.Contains(collapsed, "line-k") || !strings.Contains(collapsed, "5 more lines") {
		t.Fatalf("collapsed fallback result = %q", collapsed)
	}
	// createCallFallback for `{}`: the title alone.
	if strings.Contains(collapsed, "{}") || strings.Contains(collapsed, "=") {
		t.Fatalf("empty arguments were shown: %q", collapsed)
	}
}

// A definition with its own renderCall keeps it; the fallback is only for a missing or failing renderCall (tool-execution.ts:337-353).
func TestRegisteredToolCustomCallRendererReplacesTheFallback(t *testing.T) {
	m := newFallbackTestMode(extension.ToolDefinition{
		Name: "custom_extension",
		RenderCall: func(json.RawMessage, extension.Theme, extension.ToolRenderContext) extension.Component {
			return tui.NewText("custom call")
		},
	})
	m.handleAgentEvent(agent.ToolExecutionStartEvent{ToolCallID: "custom-1", ToolName: "custom_extension", Args: json.RawMessage(`{"a":1}`)})
	got := plainRows(m.toolByID["custom-1"].Render(120))
	if !strings.Contains(got, "custom call") || strings.Contains(got, "a=1") {
		t.Fatalf("custom call renderer card = %q", got)
	}
}

// interactive-mode.ts:3501-3503: a call another tool made through ctx.executeTool() (for example from a codemode
// script) is shown inside its parent's row, so it gets no card; its update and end events find no card either.
func TestNestedToolCallsGetNoCardOfTheirOwn(t *testing.T) {
	m := newFallbackTestMode(extension.ToolDefinition{Name: "generic_extension"})
	m.handleAgentEvent(agent.ToolExecutionStartEvent{ToolCallID: "parent", ToolName: "generic_extension", Args: json.RawMessage(`{}`)})
	before := len(m.chatContainer.Children())
	m.handleAgentEvent(agent.ToolExecutionStartEvent{ToolCallID: "parent/1", ToolName: "generic_extension", Args: json.RawMessage(`{"n":1}`), ParentToolCallID: "parent"})
	m.handleAgentEvent(agent.ToolExecutionUpdateEvent{ToolCallID: "parent/1", ToolName: "generic_extension", ParentToolCallID: "parent", PartialResult: agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "partial"}}}})
	m.handleAgentEvent(agent.ToolExecutionEndEvent{ToolCallID: "parent/1", ToolName: "generic_extension", Result: agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "nested result"}}}, ParentToolCallID: "parent"})
	if after := len(m.chatContainer.Children()); after != before {
		t.Fatalf("a nested call added %d transcript rows", after-before)
	}
	if m.toolByID["parent/1"] != nil || len(m.toolOrder) != 1 {
		t.Fatalf("nested call has a card: %v, order %d", m.toolByID["parent/1"], len(m.toolOrder))
	}
	if got := plainRows(m.toolByID["parent"].Render(120)); strings.Contains(got, "nested result") {
		t.Fatalf("the parent card shows the nested call's result: %q", got)
	}
}
