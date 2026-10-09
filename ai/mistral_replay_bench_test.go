//go:build !pig_strip_mistral_conversations

package ai

import (
	"fmt"
	"strings"
	"testing"
)

func BenchmarkMistralReplayTransform(b *testing.B) {
	model := &Model{ID: "selected-model", Input: []string{"text"}, ProviderMeta: ProviderMetadata{API: APIMistralConversations, ProviderID: "custom-mistral"}}
	provider := NewMistralProvider(MistralConfig{ModelMetadata: model, Model: model.ID, ProviderID: model.ProviderMeta.ProviderID}).(*mistralProvider)
	var messages []Message
	for i := range 64 {
		id := fmt.Sprintf("tool_call_%d", i)
		messages = append(messages,
			UserMessage{Content: UserText("question")},
			AssistantMessage{API: APIAnthropicMessages, Provider: "anthropic", Model: "claude", StopReason: StopReasonToolUse, Content: []AssistantContentBlock{ThinkingContent{Thinking: strings.Repeat("reason ", 128)}, ToolCall{ID: id, Name: "lookup", Arguments: JsonObject{"query": "question"}}}},
			ToolResultMessage{ToolCallID: id, ToolName: "lookup", Content: []ToolResultMessageContent{TextContent{Text: "answer"}}},
		)
	}
	b.ReportAllocs()
	for b.Loop() {
		normalizer := newMistralIDNormalizer()
		transformed := TransformMessages(messages, model, func(id string, _ *Model, _ AssistantMessage) string { return normalizer.normalize(id) })
		payload := mistralRequest{Model: model.ID, Stream: true, Messages: provider.convertMessages(transformed, false)}
		if _, err := toMistralWirePayload(payload); err != nil {
			b.Fatal(err)
		}
	}
}
