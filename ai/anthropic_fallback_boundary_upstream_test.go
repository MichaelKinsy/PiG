package ai

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// upstream: packages/ai/test/anthropic-sse-parsing.test.ts:174-224; packages/ai/src/api/anthropic-messages.ts:626-632. A fallback before any output is allowed; even an empty started block makes a later fallback unsupported.
func TestAnthropicFallbackBoundaryUpstream(t *testing.T) {
	for _, tc := range []struct {
		name, initial string
		started       bool
	}{
		{"before output", "", false},
		{"after empty block", "", true},
		{"after partial output", "partial", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const model = "claude-opus-5"
			provider := NewAnthropicProvider(AnthropicConfig{Model: model, ProviderID: "anthropic"}).(*anthropicProvider)
			t.Cleanup(func() {
				if err := provider.Close(); err != nil {
					t.Error(err)
				}
			})
			var sse strings.Builder
			sse.WriteString("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_fallback\",\"model\":\"claude-opus-5\",\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n")
			if tc.started {
				fmt.Fprintf(&sse, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":%q}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n", tc.initial)
			}
			sse.WriteString("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"fallback\",\"from\":{\"model\":\"claude-opus-5\"},\"to\":{\"model\":\"claude-opus-4-8\"}}}\n\n")
			if !tc.started {
				sse.WriteString("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":2,\"content_block\":{\"type\":\"text\",\"text\":\"done\"}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":2}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
			}
			builder := newAssistantStreamBuilder(t.Context(), APIAnthropicMessages, "anthropic", model)
			provider.parseAnthropicSSE(t.Context(), strings.NewReader(sse.String()), builder, anthropicStreamNames{})
			result := builder.stream.Result()
			if tc.started {
				if result.StopReason != StopReasonError || result.ErrorMessage != "Anthropic performed an unsupported mid-output model fallback" {
					t.Fatalf("fallback result = %#v", result)
				}
				if !reflect.DeepEqual(result.Content, []AssistantContentBlock{TextContent{Text: tc.initial}}) {
					t.Fatalf("fallback lost prior output: %#v", result.Content)
				}
			} else if result.StopReason != StopReasonStop || result.ErrorMessage != "" || !reflect.DeepEqual(result.Content, []AssistantContentBlock{TextContent{Text: "done"}}) {
				t.Fatalf("initial fallback result = %#v", result)
			}
		})
	}
}
