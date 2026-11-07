package codingagent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// Pi 0.87.1 core/tools/renderers/read.ts:formatReadResult and write.ts:formatWriteCall use retained call arguments, not private result metadata.
func TestInteractiveBuiltinFileResultsWithoutPrivateDetails(t *testing.T) {
	for _, tc := range []struct{ name, args, output, body string }{
		{"read", `{"path":"notes.txt"}`, "UNIQUE_READ_BODY", "UNIQUE_READ_BODY"},
		{"write", `{"path":"notes.txt","content":"UNIQUE_WRITE_CONTENT"}`, "Successfully wrote to notes.txt", "UNIQUE_WRITE_CONTENT"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := newTickRenderProbe(t, "regular")
			m.handleAgentEvent(agent.ToolExecutionStartEvent{ToolCallID: "file", ToolName: tc.name, Args: json.RawMessage(tc.args)})
			card := m.toolByID["file"]
			m.handleAgentEvent(agent.ToolExecutionEndEvent{ToolCallID: "file", ToolName: tc.name, Result: agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: tc.output}}}})
			collapsed := widthx.StripAnsi(strings.Join(card.Render(80), "\n"))
			if tc.name == "read" && strings.Contains(collapsed, tc.body) {
				t.Fatalf("collapsed read exposed body without details: %s", collapsed)
			}
			if tc.name == "write" && !strings.Contains(collapsed, tc.body) {
				t.Fatalf("write lost call-argument preview: %s", collapsed)
			}
			card.SetExpanded(true)
			expanded := widthx.StripAnsi(strings.Join(card.Render(80), "\n"))
			if !strings.Contains(expanded, tc.body) {
				t.Fatalf("expanded %s lost content: %s", tc.name, expanded)
			}
		})
	}
}
