package ai

// Ports packages/ai/test/openai-completions-non-null-assistant-content.test.ts. The compat is passed as the model's own
// compat; every flag the Pi test sets besides requiresNonNullAssistantContent and requiresAssistantAfterToolResult is
// its default.

import (
	"slices"
	"testing"
)

func nonNullAssistantContentTranscript() []Message {
	return []Message{
		UserMessage{Content: []UserContentBlock{TextContent{Text: "Run the command"}}},
		AssistantMessage{
			Content:    []AssistantContentBlock{ToolCall{ID: "tool-1", Name: "bash", Arguments: JsonObject{"command": "true"}}},
			StopReason: StopReasonToolUse,
		},
		ToolResultMessage{ToolCallID: "tool-1", ToolName: "bash", Content: []ToolResultContentBlock{TextContent{Text: "ok"}}},
		UserMessage{Content: []UserContentBlock{TextContent{Text: "Thanks"}}},
	}
}

func convertNonNullAssistantContent(t *testing.T, compat OpenAICompat) ([]string, []oaiMessage) {
	t.Helper()
	p := NewOpenAIProvider(OpenAIConfig{Model: "test", Compat: &compat}).(*openAIProvider)
	messages, err := p.convertMessagesWithCompat(nonNullAssistantContentTranscript(), nil, "system", toolAnchors{}, false)
	if err != nil {
		t.Fatal(err)
	}
	roles := make([]string, 0, len(messages))
	var assistants []oaiMessage
	for _, message := range messages {
		roles = append(roles, message.Role)
		if message.Role == "assistant" {
			assistants = append(assistants, message)
		}
	}
	return roles, assistants
}

func TestRequiresNonNullAssistantContentSendsNullContentForToolCallOnlyMessagesByDefault(t *testing.T) {
	_, assistants := convertNonNullAssistantContent(t, OpenAICompat{})
	if len(assistants) != 1 {
		t.Fatalf("assistant messages = %#v", assistants)
	}
	if assistants[0].Content != nil {
		t.Fatalf("content = %#v, want null", assistants[0].Content)
	}
}

func TestRequiresNonNullAssistantContentSendsEmptyStringWithoutInsertingAnAssistantMessage(t *testing.T) {
	roles, assistants := convertNonNullAssistantContent(t, OpenAICompat{RequiresNonNullAssistantContent: new(true)})
	if want := []string{"user", "assistant", "tool", "user"}; !slices.Equal(roles, want) {
		t.Fatalf("roles = %v, want %v", roles, want)
	}
	if len(assistants) != 1 || assistants[0].Content != "" {
		t.Fatalf("assistant messages = %#v, want one with empty string content", assistants)
	}
}

func TestRequiresNonNullAssistantContentStillInsertsAssistantMessageWhenRequiresAssistantAfterToolResult(t *testing.T) {
	roles, assistants := convertNonNullAssistantContent(t, OpenAICompat{RequiresAssistantAfterToolResult: new(true)})
	if want := []string{"user", "assistant", "tool", "assistant", "user"}; !slices.Equal(roles, want) {
		t.Fatalf("roles = %v, want %v", roles, want)
	}
	if len(assistants) != 2 || assistants[0].Content != "" || assistants[1].Content != "I have processed the tool results." {
		t.Fatalf("assistant messages = %#v", assistants)
	}
}
