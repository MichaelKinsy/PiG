//go:build !pig_strip_mistral_conversations

package ai

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// streamFrameShape is one provider's streaming response: frames SSE records that each carry a delta, and the records around them.
type streamFrameShape struct {
	name string
	// api, providerID and model name the model provider streams for: the assistant turns a request test replays come from it.
	api        API
	providerID string
	model      string
	provider   func(baseURL string) Provider
	body       func(frames int) string
	// minEvents is the number of stream events the consumer must see, so a body the provider rejects cannot pass as a fast benchmark.
	minEvents func(frames int) int
}

var streamFrameShapes = []streamFrameShape{
	{
		name: "openai-completions-text", api: APIOpenAICompletions, providerID: "openai", model: "gpt-bench",
		provider: func(url string) Provider {
			return NewOpenAIProvider(OpenAIConfig{BaseURL: url, APIKey: "k", Model: "gpt-bench", ProviderID: "openai"})
		},
		body: func(frames int) string {
			var body strings.Builder
			for i := range frames {
				fmt.Fprintf(&body, "data: {\"id\":\"chatcmpl-1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"gpt-bench\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"t%d \"},\"finish_reason\":null}]}\n\n", i)
			}
			body.WriteString("data: {\"id\":\"chatcmpl-1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"gpt-bench\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":7,\"total_tokens\":12}}\n\ndata: [DONE]\n\n")
			return body.String()
		},
		minEvents: func(frames int) int { return frames },
	},
	{
		name: "openai-completions-toolcall", api: APIOpenAICompletions, providerID: "openai", model: "gpt-bench",
		provider: func(url string) Provider {
			return NewOpenAIProvider(OpenAIConfig{BaseURL: url, APIKey: "k", Model: "gpt-bench", ProviderID: "openai"})
		},
		body: func(frames int) string {
			var body strings.Builder
			fmt.Fprintf(&body, "data: {\"id\":\"c\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"gpt-bench\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"write\",\"arguments\":\"{\\\"path\\\":\\\"a.go\\\",\\\"content\\\":\\\"\"}}]},\"finish_reason\":null}]}\n\n")
			for range frames {
				body.WriteString("data: {\"id\":\"c\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"gpt-bench\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"line of code\\\\n\"}}]},\"finish_reason\":null}]}\n\n")
			}
			body.WriteString("data: {\"id\":\"c\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"gpt-bench\",\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
			return body.String()
		},
		minEvents: func(frames int) int { return frames },
	},
	{
		name: "openai-responses-text", api: APIOpenAIResponses, providerID: "openai", model: "gpt-bench",
		provider: func(url string) Provider {
			return NewOpenAIResponsesProvider(OpenAIResponsesConfig{BaseURL: url, APIKey: "k", Model: "gpt-bench", ProviderID: "openai"})
		},
		body: func(frames int) string {
			var body strings.Builder
			body.WriteString(sseRecords(
				`data: {"type":"response.created","response":{"id":"resp_1"}}`,
				`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"msg_1","role":"assistant","content":[]}}`,
				`data: {"type":"response.content_part.added","output_index":0,"content_index":0,"part":{"type":"output_text","text":""}}`,
			))
			for i := range frames {
				fmt.Fprintf(&body, "data: {\"type\":\"response.output_text.delta\",\"output_index\":0,\"content_index\":0,\"item_id\":\"msg_1\",\"delta\":\"t%d \"}\n\n", i)
			}
			body.WriteString(sseRecords(
				`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"message","id":"msg_1","role":"assistant","content":[{"type":"output_text","text":"done"}]}}`,
				`data: {"type":"response.completed","response":{"id":"resp_1","status":"completed","usage":{"input_tokens":5,"output_tokens":7,"total_tokens":12}}}`,
			))
			return body.String()
		},
		minEvents: func(frames int) int { return frames },
	},
	{
		name: "anthropic-text", api: APIAnthropicMessages, providerID: "anthropic", model: "claude-bench",
		provider: func(url string) Provider {
			return NewAnthropicProvider(AnthropicConfig{BaseURL: url, APIKey: "k", Model: "claude-bench"})
		},
		body: func(frames int) string {
			var body strings.Builder
			body.WriteString(sseRecords(
				"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-bench\",\"content\":[],\"usage\":{\"input_tokens\":5,\"output_tokens\":1}}}",
				"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}",
			))
			for i := range frames {
				fmt.Fprintf(&body, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"t%d \"}}\n\n", i)
			}
			body.WriteString(sseRecords(
				"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}",
				"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":7}}",
				"event: message_stop\ndata: {\"type\":\"message_stop\"}",
			))
			return body.String()
		},
		minEvents: func(frames int) int { return frames },
	},
	{
		name: "anthropic-toolcall", api: APIAnthropicMessages, providerID: "anthropic", model: "claude-bench",
		provider: func(url string) Provider {
			return NewAnthropicProvider(AnthropicConfig{BaseURL: url, APIKey: "k", Model: "claude-bench"})
		},
		body: func(frames int) string {
			var body strings.Builder
			body.WriteString(sseRecords(
				"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-bench\",\"content\":[],\"usage\":{\"input_tokens\":5,\"output_tokens\":1}}}",
				"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_1\",\"name\":\"write\",\"input\":{}}}",
				"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"path\\\":\\\"a.go\\\",\\\"content\\\":\\\"\"}}",
			))
			for range frames {
				body.WriteString("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"line of code\\\\n\"}}\n\n")
			}
			body.WriteString(sseRecords(
				"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"\\\"}\"}}",
				"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}",
				"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":7}}",
				"event: message_stop\ndata: {\"type\":\"message_stop\"}",
			))
			return body.String()
		},
		minEvents: func(frames int) int { return frames },
	},
	{
		name: "google-text", api: APIGoogleGenerativeAI, providerID: "google", model: "gemini-2.5-flash",
		provider: func(url string) Provider {
			return NewGoogleProvider(GoogleConfig{BaseURL: url, APIKey: "k", Model: "gemini-2.5-flash", ProviderID: "google"})
		},
		body: func(frames int) string {
			var body strings.Builder
			for i := range frames {
				fmt.Fprintf(&body, "data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"t%d \"}]}}]}\r\n\r\n", i)
			}
			body.WriteString("data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"end\"}]},\"finishReason\":\"STOP\"}]}\r\n\r\n")
			return body.String()
		},
		minEvents: func(frames int) int { return frames },
	},
	{
		name: "mistral-text", api: APIMistralConversations, providerID: "mistral", model: "mistral-bench",
		provider: func(url string) Provider {
			return NewMistralProvider(MistralConfig{BaseURL: url, APIKey: "k", Model: "mistral-bench", ProviderID: "mistral"})
		},
		body: func(frames int) string {
			var body strings.Builder
			for i := range frames {
				fmt.Fprintf(&body, "data: {\"id\":\"m1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"mistral-bench\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"t%d \"},\"finish_reason\":null}]}\n\n", i)
			}
			body.WriteString("data: {\"id\":\"m1\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"mistral-bench\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":7,\"total_tokens\":12}}\n\ndata: [DONE]\n\n")
			return body.String()
		},
		minEvents: func(frames int) int { return frames },
	},
}

// streamFrameRun streams one response of frames records through the provider and drains the events. It returns the number of events.
func streamFrameRun(b testing.TB, provider Provider, options StreamOptions) int {
	stream, err := provider.Stream(context.Background(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi")}}}), options)
	if err != nil {
		b.Fatal(err)
	}
	events := 0
	for range stream.Events(context.Background()) {
		events++
	}
	return events
}

// BenchmarkProviderStreamFrames is the streaming-path gate: allocations and bytes per frame for each provider's SSE response of 5,000 delta records, through the real HTTP client, decoder, builder and event stream, to a plain consumer. A frame is one delta record. The B/frame and allocs/frame metrics are what a count gate ratchets.
func BenchmarkProviderStreamFrames(b *testing.B) {
	const frames = 5000
	for _, shape := range streamFrameShapes {
		b.Run(shape.name, func(b *testing.B) {
			payload := shape.body(frames)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, payload)
			}))
			defer server.Close()
			provider := shape.provider(server.URL)
			if events := streamFrameRun(b, provider, StreamOptions{}); events < shape.minEvents(frames) {
				b.Fatalf("%s: %d events from %d frames", shape.name, events, frames)
			}
			b.ReportAllocs()
			for b.Loop() {
				streamFrameRun(b, provider, StreamOptions{})
			}
			// b.AllocsPerOp and bytes are per whole stream; the per-frame metrics divide by the frame count.
			b.ReportMetric(float64(frames), "frames/op")
		})
	}
}
