package ai

import (
	"reflect"
	"testing"
)

// Ports packages/ai/test/transform-messages-cross-model-thinking.test.ts (Pi #11000, #10995): thinking from another model is dropped, not
// sent as text; text and tool calls stay.
func crossModelThinkingTarget() *Model {
	return &Model{ID: "moonshotai/Kimi-K3", DisplayName: "Kimi K3", Input: []string{"text", "image"}, ProviderMeta: ProviderMetadata{ProviderID: "sference", API: APIOpenAICompletions, BaseURL: "https://api.sference.com/v1"}}
}

func crossModelThinkingAssistant(model string, stop StopReason, content ...AssistantContentBlock) AssistantMessage {
	return AssistantMessage{API: APIOpenAICompletions, Provider: "sference", Model: model, StopReason: stop, Content: content}
}

func TestTransformMessagesCrossModelThinking(t *testing.T) {
	kimi := crossModelThinkingTarget()
	// transform-messages-cross-model-thinking.test.ts:39 "does not send another model's readable thinking as text"
	t.Run("does not send another model's readable thinking as text", func(t *testing.T) {
		messages := []Message{
			UserMessage{Content: UserText("Why does curl work but node fails?")},
			crossModelThinkingAssistant("zai-org/GLM-5.3", StopReasonToolUse,
				ThinkingContent{Thinking: "The user asks about ECONNRESET. Candidates: proxy, IPv6, TLS.", ThinkingSignature: "reasoning_content"},
				TextContent{Text: "Let me check the TLS versions."},
				ToolCall{ID: "call_1", Name: "bash", Arguments: JsonObject{"command": "curl -v"}}),
			ToolResultMessage{ToolCallID: "call_1", ToolName: "bash", Content: []ToolResultMessageContent{TextContent{Text: "TLSv1.2"}}},
		}
		result := TransformMessages(messages, kimi, nil)
		var got []AssistantContentBlock
		for _, message := range result {
			if assistant, ok := message.(AssistantMessage); ok {
				got = assistant.Content
			}
		}
		want := []AssistantContentBlock{TextContent{Text: "Let me check the TLS versions."}, ToolCall{ID: "call_1", Name: "bash", Arguments: JsonObject{"command": "curl -v"}}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("assistant content = %#v, want %#v", got, want)
		}
	})
	// transform-messages-cross-model-thinking.test.ts:70 "keeps thinking only from the target model when models alternate"
	t.Run("keeps thinking only from the target model when models alternate", func(t *testing.T) {
		thinking := func(text string) ThinkingContent {
			return ThinkingContent{Thinking: text, ThinkingSignature: "reasoning_content"}
		}
		turn := func(model string, thought string, answer string) AssistantMessage {
			return crossModelThinkingAssistant(model, StopReasonStop, thinking(thought), TextContent{Text: answer})
		}
		user := func(text string) Message { return UserMessage{Content: UserText(text)} }
		messages := []Message{
			user("1"), turn("moonshotai/Kimi-K3", "kimi 1", "a"),
			user("2"), turn("zai-org/GLM-5.3", "glm 1", "b"),
			user("3"), turn("moonshotai/Kimi-K3", "kimi 2", "c"),
			user("4"), turn("zai-org/GLM-5.3", "glm 2", "d"),
			user("hi"),
		}
		var got [][]AssistantContentBlock
		for _, message := range TransformMessages(messages, kimi, nil) {
			if assistant, ok := message.(AssistantMessage); ok {
				got = append(got, assistant.Content)
			}
		}
		want := [][]AssistantContentBlock{
			{thinking("kimi 1"), TextContent{Text: "a"}},
			{TextContent{Text: "b"}},
			{thinking("kimi 2"), TextContent{Text: "c"}},
			{TextContent{Text: "d"}},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("assistant contents = %#v, want %#v", got, want)
		}
	})
}
