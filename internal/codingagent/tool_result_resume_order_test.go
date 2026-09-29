package codingagent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

// Pi core/tools/render-utils.ts:45-48 joins text blocks with newlines in both live and reconstructed tool cards.
func TestResumedToolResultUsesOrderedTextProjection(t *testing.T) {
	call := ai.ToolCall{ID: "ordered", Name: "custom_ordered", Arguments: ai.JsonObject{}}
	content := []ai.ToolResultMessageContent{ai.TextContent{Text: "before"}, ai.ImageContent{Data: "bm90LWFuLWltYWdl", MimeType: "image/png"}, ai.TextContent{Text: ""}, ai.TextContent{Text: "after"}}
	m := resumeThinkingMode(t, false, userMsg("question"), assistantMsg("", call), agent.AgentMessage{ToolResult: &agent.ToolResultMessage{Role: agent.RoleToolResult, ToolCallID: call.ID, ToolName: call.Name, Content: content}})
	m.renderSessionEntries()
	previous, last := m.chatContainer.LastTwoChildren()
	for _, component := range []tui.Component{previous, last} {
		if tool, ok := component.(*tui.ToolExecutionComponent); ok {
			if tool.Output != "before\n\nafter" {
				t.Fatalf("resumed tool output=%q, want text blocks separated by newlines", tool.Output)
			}
			return
		}
	}
	t.Fatal("resumed tool card missing")
}
