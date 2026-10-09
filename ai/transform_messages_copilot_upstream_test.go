package ai

import "testing"

// Ports packages/ai/test/transform-messages-copilot-openai-to-anthropic.test.ts against TransformMessages with the
// Anthropic tool-call ID normalizer, as anthropic-messages.ts buildParams calls it.
func TestTransformMessagesCopilotOpenAIToAnthropicUpstream(t *testing.T) {
	model := &Model{ID: "claude-sonnet-4.6", DisplayName: "Claude Sonnet 4.6", Input: []string{"text", "image"}, ProviderMeta: ProviderMetadata{API: "anthropic-messages", ProviderID: "github-copilot", BaseURL: "https://api.individual.githubcopilot.com", Reasoning: true}, Capabilities: ModelCapabilities{ContextWindow: 128000, MaxOutputTokens: 16000}}
	normalize := func(id string, _ *Model, _ AssistantMessage) string { return normalizeAnthropicToolCallID(id) }
	assistant := func(content ...AssistantContentBlock) AssistantMessage {
		return AssistantMessage{Content: content, API: "openai-responses", Provider: "github-copilot", Model: "gpt-5", StopReason: StopReasonToolUse, Timestamp: 1}
	}
	user := func(text string) UserMessage { return UserMessage{Content: UserText(text), Timestamp: 1} }
	toolResult := func(id, name, text string) ToolResultMessage {
		return ToolResultMessage{ToolCallID: id, ToolName: name, Content: []ToolResultMessageContent{TextContent{Text: text}}, Timestamp: 1}
	}
	findAssistant := func(t *testing.T, messages []Message) AssistantMessage {
		t.Helper()
		for _, message := range messages {
			if assistant, ok := message.(AssistantMessage); ok {
				return assistant
			}
		}
		t.Fatalf("no assistant message in %+v", messages)
		return AssistantMessage{}
	}

	// transform-messages-copilot-openai-to-anthropic.test.ts:50
	t.Run("converts thinking blocks to plain text when source model differs", func(t *testing.T) {
		source := AssistantMessage{Content: []AssistantContentBlock{ThinkingContent{Thinking: "Let me think about this...", ThinkingSignature: "reasoning_content"}, TextContent{Text: "Hi there!"}}, API: "openai-completions", Provider: "github-copilot", Model: "gpt-4o", StopReason: StopReasonStop, Timestamp: 1}
		result := findAssistant(t, TransformMessages([]Message{user("hello"), source}, model, normalize))
		texts, thinking := 0, 0
		for _, block := range result.Content {
			switch block.(type) {
			case TextContent:
				texts++
			case ThinkingContent:
				thinking++
			}
		}
		if thinking != 0 || texts < 2 {
			t.Fatalf("content = %+v", result.Content)
		}
	})
	// transform-messages-copilot-openai-to-anthropic.test.ts:90
	t.Run("removes thoughtSignature from tool calls when migrating between models", func(t *testing.T) {
		call := ToolCall{ID: "call_123", Name: "bash", Arguments: JsonObject{"command": "ls"}, ThoughtSignature: `{"type":"reasoning.encrypted","id":"call_123","data":"encrypted"}`}
		result := findAssistant(t, TransformMessages([]Message{user("run a command"), assistant(call), toolResult("call_123", "bash", "output")}, model, normalize))
		for _, block := range result.Content {
			if toolCall, ok := block.(ToolCall); ok && toolCall.ThoughtSignature != "" {
				t.Fatalf("thoughtSignature = %q", toolCall.ThoughtSignature)
			}
		}
	})
	// transform-messages-copilot-openai-to-anthropic.test.ts:136
	t.Run("adds synthetic tool results for trailing orphaned tool calls", func(t *testing.T) {
		result := TransformMessages([]Message{user("read the file"), assistant(ToolCall{ID: "call_123|fc_123", Name: "read", Arguments: JsonObject{"path": "README.md"}})}, model, normalize)
		last, ok := result[len(result)-1].(ToolResultMessage)
		if !ok || last.ToolCallID != "call_123_fc_123" || last.ToolName != "read" || !last.IsError || len(last.Content) != 1 || last.Content[0] != (TextContent{Text: "No result provided"}) {
			t.Fatalf("last message = %+v", result[len(result)-1])
		}
	})
	// transform-messages-copilot-openai-to-anthropic.test.ts:162
	t.Run("adds synthetic results only for trailing tool calls that are still missing results", func(t *testing.T) {
		result := TransformMessages([]Message{
			user("run commands"),
			assistant(ToolCall{ID: "call_1|fc_1", Name: "read", Arguments: JsonObject{"path": "README.md"}}, ToolCall{ID: "call_2|fc_2", Name: "bash", Arguments: JsonObject{"command": "pwd"}}),
			toolResult("call_1|fc_1", "read", "done"),
		}, model, normalize)
		var synthetic []ToolResultMessage
		for _, message := range result {
			if toolResult, ok := message.(ToolResultMessage); ok && toolResult.IsError {
				synthetic = append(synthetic, toolResult)
			}
		}
		if len(synthetic) != 1 || synthetic[0].ToolCallID != "call_2_fc_2" || synthetic[0].ToolName != "bash" || len(synthetic[0].Content) != 1 || synthetic[0].Content[0] != (TextContent{Text: "No result provided"}) {
			t.Fatalf("synthetic results = %+v", synthetic)
		}
	})
}
