package agent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// A provider may number its tool calls per response, so call id "0" opens a new call every turn. The result of the second turn's call arrives after the next user message; the slot of that call must carry the second result ("two"), not the first turn's result that already answered the first call ("one").
func TestLateResultRecoveryTakesTheResultOfTheOpenCall(t *testing.T) {
	call := func() AgentMessage {
		return AgentMessage{Assistant: &AssistantMessage{Role: RoleAssistant, StopReason: ai.StopReasonToolUse, Content: []ai.AssistantContentBlock{ai.ToolCall{ID: "0", Name: "read", Arguments: ai.JsonObject{}}}}}
	}
	result := func(text string) AgentMessage {
		return AgentMessage{ToolResult: &ToolResultMessage{Role: RoleToolResult, ToolCallID: "0", ToolName: "read", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: text}}}}
	}
	user := func(text string) AgentMessage {
		return AgentMessage{User: &UserMessage{Role: "user", Content: ai.UserText(text)}}
	}
	got := NormalizeMessages([]AgentMessage{user("go"), call(), result("one"), call(), user("next"), result("two")}, &ai.Model{ID: "m"})
	var texts []string
	for _, message := range got {
		if message.ToolResult != nil {
			texts = append(texts, message.ToolResult.Content[0].(ai.TextContent).Text)
		}
	}
	if len(texts) != 2 || texts[0] != "one" || texts[1] != "two" {
		t.Fatalf("tool result texts = %q, want [one two] (roles %v)", texts, normalizedRoles(got))
	}
}

func normalizedRoles(messages []AgentMessage) []string {
	var roles []string
	for _, message := range messages {
		switch {
		case message.User != nil:
			roles = append(roles, "user")
		case message.Assistant != nil:
			roles = append(roles, "assistant")
		case message.ToolResult != nil:
			roles = append(roles, "toolResult")
		default:
			roles = append(roles, "other")
		}
	}
	return roles
}
