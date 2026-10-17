package codingagent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
	"github.com/MichaelKinsy/PiG/tui"
)

// A finished read card keeps the file text once: the result it holds has no structured content (read's is the text as JSON, which
// toReadOutput derives from the content; Pi's card holds the text string itself), and a renderer, built in or an extension's, still
// receives the structured content the tool returned, also one bound after the result arrived.
func TestReadCardKeepsTheTextOnceAndRenderersGetStructuredContent(t *testing.T) {
	tui.SetTheme("dark")
	m, _ := newTickRenderProbe(t, "regular")
	var answered atomic.Bool
	var seen []json.RawMessage
	resolver := func(toolName string, next func() *extension.ToolRenderers) *extension.ToolRenderers {
		if toolName != "read" || !answered.Load() {
			return next()
		}
		return &extension.ToolRenderers{RenderResult: func(result extension.AgentToolResult, _ extension.ToolRenderResultOptions, _ extension.Theme, _ extension.ToolRenderContext) extension.Component {
			seen = append(seen, result.StructuredContent)
			return tui.NewPaddedText("extension rendered", 0, 0, nil)
		}}
	}
	m.newRunner = inproc.NewRunner([]extension.Extension{{Name: "late", ToolRenderers: []extension.ToolRendererResolver{resolver}}}, t.TempDir())

	dir := t.TempDir()
	text := strings.Repeat("a line of the file with \"quotes\" and a \\ backslash\n", 200)
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	args := json.RawMessage(`{"path":"file.txt"}`)
	result, err := (&tools.ReadTool{CWD: dir}).Execute(t.Context(), "call-1", args, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.StructuredContent) == 0 {
		t.Fatal("the read tool returned no structured content")
	}
	want := append(json.RawMessage(nil), result.StructuredContent...)

	partial := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.ToolCall{ID: "call-1", Name: "read"}}}
	m.handleAgentEvent(agent.MessageUpdateEvent{AssistantMessageEvent: ai.ToolCallDeltaEvent{ContentIndex: 0, Delta: string(args), Partial: partial}})
	m.handleAgentEvent(agent.MessageEndEvent{Message: agent.AgentMessage{Assistant: &agent.AssistantMessage{StopReason: ai.StopReasonToolUse}}})
	m.handleAgentEvent(agent.ToolExecutionStartEvent{ToolCallID: "call-1", ToolName: "read", Args: args})
	m.handleAgentEvent(agent.ToolExecutionEndEvent{ToolCallID: "call-1", ToolName: "read", Result: result})

	var card *tui.ToolExecutionComponent
	for _, child := range m.chatContainer.Children() {
		if c, ok := child.(*tui.ToolExecutionComponent); ok {
			card = c
		}
	}
	if card == nil {
		t.Fatal("no read card")
	}
	if held, ok := card.ResultValue().(agent.AgentToolResult); ok && len(held.StructuredContent) > 0 {
		t.Fatalf("the card holds the read result with its %d-byte structured content", len(held.StructuredContent))
	}
	if got, ok := toolResultOf(card.ResultValue()); !ok || string(got.StructuredContent) != string(want) {
		t.Fatalf("the card's result gives structured content %q, want the tool's", got.StructuredContent)
	}
	_ = card.Render(100)

	answered.Store(true)
	m.reapplyToolPresentation("read")
	if got := stripANSITest(strings.Join(card.Render(100), "\n")); !strings.Contains(got, "extension rendered") {
		t.Fatalf("card after the extension renderer = %q", got)
	}
	if len(seen) == 0 || string(seen[len(seen)-1]) != string(want) {
		t.Fatalf("the extension renderer received structured content %q, want the read tool's", seen)
	}
}

// A result whose structured content is not the one its tool derives from the content (an extension's own read tool) is kept whole.
func TestToolCardKeepsStructuredContentItCannotDerive(t *testing.T) {
	result := agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "text"}}, StructuredContent: json.RawMessage(`{"own":true}`)}
	for _, tool := range []string{"read", "bash", "ext_tool"} {
		held, ok := retainToolResult(tool, result).(agent.AgentToolResult)
		if !ok || string(held.StructuredContent) != `{"own":true}` {
			t.Fatalf("%s: card keeps %#v", tool, retainToolResult(tool, result))
		}
	}
}
