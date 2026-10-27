package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// BenchmarkStreamToolCallArguments streams one write call whose argument text arrives in 32-byte deltas through each provider path, and reads the result. The cost per byte of arguments is flat when each delta is read once and grows with the text when the accumulated text is parsed per delta.
func BenchmarkStreamToolCallArguments(b *testing.B) {
	var content strings.Builder
	for line := 0; content.Len() < 250_000; line++ {
		fmt.Fprintf(&content, "const line%d = \"value %d\"; // \\ done\n", line, line)
	}
	sse := func(event string, data any) string {
		encoded, _ := json.Marshal(data)
		if event != "" {
			return "event: " + event + "\ndata: " + string(encoded) + "\n\n"
		}
		return "data: " + string(encoded) + "\n\n"
	}
	bodies := map[string]func(deltas []string, text string) (string, func(url string) Provider){
		"anthropic": func(deltas []string, _ string) (string, func(string) Provider) {
			var body strings.Builder
			body.WriteString(sse("message_start", map[string]any{"type": "message_start", "message": map[string]any{"id": "m", "model": "c", "usage": map[string]any{"input_tokens": 1, "output_tokens": 1}}}))
			body.WriteString(sse("content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "tool_use", "id": "t", "name": "write", "input": map[string]any{}}}))
			for _, delta := range deltas {
				body.WriteString(sse("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "input_json_delta", "partial_json": delta}}))
			}
			body.WriteString(sse("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0}))
			body.WriteString(sse("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "tool_use"}, "usage": map[string]any{"output_tokens": 1}}))
			body.WriteString(sse("message_stop", map[string]any{"type": "message_stop"}))
			return body.String(), func(url string) Provider {
				return NewAnthropicProvider(AnthropicConfig{BaseURL: url, APIKey: "k", Model: "claude-test"})
			}
		},
		"openai-completions": func(deltas []string, _ string) (string, func(string) Provider) {
			var body strings.Builder
			chunk := func(fields map[string]any) string {
				fields["index"] = 0
				return sse("", map[string]any{"id": "c", "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"tool_calls": []any{fields}}}}})
			}
			body.WriteString(chunk(map[string]any{"id": "call_1", "type": "function", "function": map[string]any{"name": "write", "arguments": ""}}))
			for _, delta := range deltas {
				body.WriteString(chunk(map[string]any{"function": map[string]any{"arguments": delta}}))
			}
			body.WriteString(sse("", map[string]any{"id": "c", "choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "tool_calls"}}}))
			body.WriteString("data: [DONE]\n\n")
			return body.String(), func(url string) Provider {
				return NewOpenAIProvider(OpenAIConfig{BaseURL: url, APIKey: "k", Model: "gpt-test", ProviderID: "openai"})
			}
		},
		"openai-responses": func(deltas []string, text string) (string, func(string) Provider) {
			var body strings.Builder
			body.WriteString(sse("", map[string]any{"type": "response.created", "response": map[string]any{"id": "r"}}))
			body.WriteString(sse("", map[string]any{"type": "response.output_item.added", "output_index": 0, "item": map[string]any{"type": "function_call", "id": "fc", "call_id": "c", "name": "write", "arguments": ""}}))
			for _, delta := range deltas {
				body.WriteString(sse("", map[string]any{"type": "response.function_call_arguments.delta", "output_index": 0, "item_id": "fc", "delta": delta}))
			}
			body.WriteString(sse("", map[string]any{"type": "response.function_call_arguments.done", "output_index": 0, "arguments": text}))
			body.WriteString(sse("", map[string]any{"type": "response.output_item.done", "output_index": 0, "item": map[string]any{"type": "function_call", "id": "fc", "call_id": "c", "name": "write", "arguments": text}}))
			body.WriteString(sse("", map[string]any{"type": "response.completed", "response": map[string]any{"id": "r", "status": "completed", "usage": map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}}}))
			return body.String(), func(url string) Provider {
				return NewOpenAIResponsesProvider(OpenAIResponsesConfig{BaseURL: url, APIKey: "k", Model: "gpt-test", ProviderID: "openai"})
			}
		},
	}
	for _, name := range []string{"anthropic", "openai-completions", "openai-responses"} {
		for _, size := range []int{25_000, 250_000} {
			b.Run(fmt.Sprintf("%s/%dKB", name, size/1000), func(b *testing.B) {
				encoded, _ := json.Marshal(content.String()[:size])
				text := `{"path":"/src/file.ts","content":` + string(encoded) + `}`
				var deltas []string
				for index := 0; index < len(text); index += 32 {
					deltas = append(deltas, text[index:min(index+32, len(text))])
				}
				body, newProvider := bodies[name](deltas, text)
				server := sequentialToolCallServer(body)
				defer server.Close()
				provider := newProvider(server.URL)
				b.SetBytes(int64(len(text)))
				b.ReportAllocs()
				for b.Loop() {
					stream := sequentialToolCallStream(b, provider)
					for range stream.Events(context.Background()) {
					}
					if result := stream.Result(); result.StopReason != StopReasonToolUse {
						b.Fatalf("stop reason %s: %s", result.StopReason, result.ErrorMessage)
					}
				}
			})
		}
	}
}
