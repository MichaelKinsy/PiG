package codingagent

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// appendTurnMessages are the messages one tool-calling turn persists: the user's prompt, an assistant message with a tool call and usage, and a large tool result.
func appendTurnMessages(i int) []agent.AgentMessage {
	usage := &ai.Usage{Input: 1200, Output: 80, CacheRead: 4000, TotalTokens: 5280}
	return []agent.AgentMessage{
		{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserText("read the file and summarize it"), Timestamp: int64(i)}},
		{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Provider: "faux", ModelID: "faux-1", Usage: usage, StopReason: ai.StopReasonToolUse, Timestamp: int64(i),
			Content: []ai.AssistantContentBlock{ai.TextContent{Text: "I will read it."}, ai.ToolCall{ID: "call_1", Name: "read", Arguments: map[string]any{"path": "main.go"}}}}},
		{ToolResult: &agent.ToolResultMessage{Role: agent.RoleToolResult, ToolCallID: "call_1", ToolName: "read", Timestamp: int64(i),
			Content: []ai.ToolResultMessageContent{ai.TextContent{Text: strings.Repeat("package main // line\n", 2500)}}, Details: map[string]any{"truncation": map[string]any{"truncated": false, "lines": 2500}}}},
	}
}

func BenchmarkAppendTurnMessages(b *testing.B) {
	session := NewSession("bench", "/project")
	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		for _, message := range appendTurnMessages(i) {
			if _, err := session.AppendMessage(message); err != nil {
				b.Fatal(err)
			}
		}
	}
}
