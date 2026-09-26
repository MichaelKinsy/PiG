package codingagent

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
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

// D75: an override of a built-in tool that supplies only one
// renderer keeps the built-in card; with both it draws them.
func TestBuiltInOverrideNeedsBothRenderers(t *testing.T) {
	call := func(json.RawMessage, extension.Theme, extension.ToolRenderContext) extension.Component {
		return tui.NewText("x")
	}
	result := func(extension.AgentToolResult, extension.ToolRenderResultOptions, extension.Theme, extension.ToolRenderContext) extension.Component {
		return tui.NewText("y")
	}
	if usesToolDefinitionRenderers("bash", extension.ToolDefinition{RenderCall: call}) {
		t.Fatal("a built-in override with only renderCall replaced the built-in card")
	}
	if !usesToolDefinitionRenderers("bash", extension.ToolDefinition{RenderCall: call, RenderResult: result}) {
		t.Fatal("a built-in override with both renderers kept the built-in card")
	}
	if !usesToolDefinitionRenderers("custom", extension.ToolDefinition{RenderResult: result}) {
		t.Fatal("an extension tool with renderResult did not draw its definition")
	}
	if usesToolDefinitionRenderers("custom", extension.ToolDefinition{}) {
		t.Fatal("an extension tool without renderers left the generic details card (D59)")
	}
}
