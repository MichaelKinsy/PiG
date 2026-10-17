package codingagent

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Appending a message counts it from the decoded entry; reloading the file counts the record. Both must give the same accounting for every kind of message, and the same conversation gate.
func TestAppendedMessagesAreCountedAsTheirRecordsAre(t *testing.T) {
	usage := func(input, cacheRead int) *ai.Usage {
		return &ai.Usage{Input: input, Output: 20, CacheRead: cacheRead, TotalTokens: input + 20 + cacheRead, Cost: ai.UsageCost{Total: 0.01}}
	}
	call := ai.ToolCall{ID: "c1", Name: "read", Arguments: map[string]any{"path": "a"}}
	messages := []agent.AgentMessage{
		{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserText("go"), Timestamp: 1}},
		{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Provider: "p", ModelID: "m", Usage: usage(1000, 0), StopReason: ai.StopReasonToolUse, Timestamp: 2,
			Content: []ai.AssistantContentBlock{ai.TextContent{Text: "a"}, call, ai.ToolCall{ID: "c2", Name: "bash"}}}},
		{ToolResult: &agent.ToolResultMessage{Role: agent.RoleToolResult, ToolCallID: "c1", ToolName: "read", Usage: usage(5, 0), Timestamp: 3,
			Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "x"}}}},
		{ToolResult: &agent.ToolResultMessage{Role: agent.RoleToolResult, ToolCallID: "c2", ToolName: "bash", Timestamp: 4, Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "y"}}}},
		{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Provider: "p", ModelID: "m", ResponseModel: "routed", Usage: usage(300, 60000), StopReason: ai.StopReasonStop, Timestamp: 5,
			Content: []ai.AssistantContentBlock{ai.TextContent{Text: "done"}}}},
		{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Provider: "p", ModelID: "m", StopReason: ai.StopReasonError, ErrorMessage: "boom", Timestamp: 6}},
		{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Provider: "p", ModelID: "m", Usage: usage(65000, 0), StopReason: ai.StopReasonStop, Timestamp: 7,
			Content: []ai.AssistantContentBlock{ai.TextContent{Text: "again"}}}},
		{Custom: map[string]any{"role": agent.RoleCustom, "customType": "note", "content": "hello", "display": true, "timestamp": float64(8)}},
	}
	session := NewSession("accounting", "/project")
	for _, message := range messages {
		if _, err := session.AppendMessage(message); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := session.AppendMessage(extension.CustomMessage{CustomType: "n", Content: "c", Display: true}); err != nil {
		t.Fatal(err)
	}
	reloaded := sessionAccountingAccumulator{}
	for _, entry := range session.GetEntries() {
		reloaded.add(entry.Raw(), entry.Base().Type)
	}
	got, want := session.Accounting(), reloaded.snapshot()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("appended accounting = %+v\nreloaded accounting = %+v", got, want)
	}
	if got.TotalMessages != 8 || got.ToolCalls != 2 || got.CacheWaste.MissCount == 0 {
		t.Fatalf("the corpus does not exercise the accounting: %+v", got)
	}
}

// The conversation gate that creates the session file opens on a user or assistant message only (session-manager.ts _hasConversation).
func TestAppendedMessagesOpenTheConversationGateOnlyForUserAndAssistant(t *testing.T) {
	for role, message := range map[string]agent.AgentMessage{
		"custom":     {Custom: map[string]any{"role": agent.RoleCustom, "customType": "n", "content": "c", "display": true, "timestamp": float64(1)}},
		"toolResult": {ToolResult: &agent.ToolResultMessage{Role: agent.RoleToolResult, ToolCallID: "c", ToolName: "t", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "x"}}}},
		"user":       {User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserText("hi")}},
		"assistant":  {Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Content: []ai.AssistantContentBlock{ai.TextContent{Text: "hi"}}, StopReason: ai.StopReasonStop}},
	} {
		session := NewSession("gate", "/project")
		if _, err := session.AppendMessage(message); err != nil {
			t.Fatal(err)
		}
		session.mu.RLock()
		opened := session.hasConversation
		session.mu.RUnlock()
		if want := role == "user" || role == "assistant"; opened != want {
			t.Errorf("%s: gate open = %v, want %v", role, opened, want)
		}
	}
}
