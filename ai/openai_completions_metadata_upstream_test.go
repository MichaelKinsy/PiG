package ai

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAICompletionsRawStopReasonUpstream(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		stop      StopReason
		message   string
	}{
		// .upstream/v0.87.1/packages/ai/test/openai-completions-raw-stop-reason.test.ts:61
		{"preserves raw finish reasons for successful stops", "stop", StopReasonStop, ""},
		// .upstream/v0.87.1/packages/ai/test/openai-completions-raw-stop-reason.test.ts:71
		{"preserves raw finish reasons for provider error stops", "content_filter", StopReasonError, "Provider finish_reason: content_filter"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sse := fmt.Sprintf("data: {\"id\":\"chatcmpl-1\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":%q}]}\n\n", tc.raw)
			provider := NewOpenAIProvider(OpenAIConfig{Model: "test-model", APIKey: "test"})
			for _, wire := range []bool{false, true} {
				t.Run(fmt.Sprintf("wire=%v", wire), func(t *testing.T) {
					message := metadataStreamResult(t, provider, sse, wire)
					if message.StopReason != tc.stop || message.RawStopReason != tc.raw || message.ErrorMessage != tc.message {
						t.Fatalf("message = %#v", message)
					}
				})
			}
		})
	}
}

func TestOpenAICompletionsResponseModelUpstream(t *testing.T) {
	for _, tc := range []struct{ name, chunks, want string }{
		// .upstream/v0.87.1/packages/ai/test/openai-completions-response-model.test.ts:61
		{"surfaces routed chunk.model on responseModel without changing model", `{"id":"chatcmpl-1","model":"anthropic/claude-opus-4.8","choices":[{"index":0,"delta":{"content":"hi"}}]}
{"id":"chatcmpl-1","model":"anthropic/claude-opus-4.8","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":0},"completion_tokens_details":{"reasoning_tokens":0}}}`, "anthropic/claude-opus-4.8"},
		// .upstream/v0.87.1/packages/ai/test/openai-completions-response-model.test.ts:89
		{"leaves responseModel undefined when chunks echo the requested id", `{"id":"chatcmpl-2","model":"openrouter/auto","choices":[{"index":0,"delta":{"content":"hi"}}]}
{"id":"chatcmpl-2","model":"openrouter/auto","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"prompt_tokens_details":{"cached_tokens":0},"completion_tokens_details":{"reasoning_tokens":0}}}`, ""},
		// .upstream/v0.87.1/packages/ai/test/openai-completions-response-model.test.ts:115
		{"ignores empty or missing chunk.model", `{"id":"chatcmpl-3","choices":[{"index":0,"delta":{"content":"hi"}}]}
{"id":"chatcmpl-3","model":"","choices":[{"index":0,"delta":{"content":"!"}}]}
{"id":"chatcmpl-3","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":2,"prompt_tokens_details":{"cached_tokens":0},"completion_tokens_details":{"reasoning_tokens":0}}}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var sse strings.Builder
			for chunk := range strings.SplitSeq(tc.chunks, "\n") {
				fmt.Fprintf(&sse, "data: %s\n\n", chunk)
			}
			for _, wire := range []bool{false, true} {
				t.Run(fmt.Sprintf("wire=%v", wire), func(t *testing.T) {
					provider := NewOpenAIProvider(OpenAIConfig{Model: "openrouter/auto", ProviderID: "openrouter", APIKey: "test"})
					message := metadataStreamResult(t, provider, sse.String(), wire)
					if message.Model != "openrouter/auto" || message.Provider != "openrouter" || message.ResponseModel != tc.want || message.StopReason != StopReasonStop {
						t.Fatalf("message = %#v", message)
					}
				})
			}
		})
	}
}

// metadataStreamResult exercises either the pure parser or the provider's HTTP caller boundary with the same upstream chunks.
func metadataStreamResult(t *testing.T, provider Provider, sse string, wire bool) *AssistantMessage {
	t.Helper()
	if !wire {
		builder := newAssistantStreamBuilder(t.Context(), APIOpenAICompletions, provider.ID(), provider.(*openAIProvider).cfg.Model)
		provider.(*openAIProvider).parseSSE(t.Context(), strings.NewReader(sse), builder, nil)
		return builder.stream.Result()
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(sse))
	}))
	defer server.Close()
	provider.(*openAIProvider).cfg.BaseURL = server.URL
	stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi")}}}), StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return stream.Result()
}

// BenchmarkOpenAICompletionsMetadataStream models a routed completion with 1024 text chunks followed by a terminal usage chunk. The parser owns no new goroutine or buffer lifetime; metadata is retained only in the result and its queued event snapshots.
func BenchmarkOpenAICompletionsMetadataStream(b *testing.B) {
	chunk := "data: {\"id\":\"chatcmpl-1\",\"model\":\"routed-model\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"word \"}}]}\n\n"
	sse := strings.Repeat(chunk, 1024) + "data: {\"id\":\"chatcmpl-1\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":128,\"completion_tokens\":1024}}\n\n"
	provider := &openAIProvider{cfg: OpenAIConfig{Model: "openrouter/auto", ProviderID: "openrouter"}}
	b.ReportAllocs()
	b.SetBytes(int64(len(sse)))
	for b.Loop() {
		builder := newAssistantStreamBuilder(b.Context(), APIOpenAICompletions, "openrouter", "openrouter/auto")
		provider.parseSSE(b.Context(), strings.NewReader(sse), builder, nil)
		if result := builder.stream.Result(); result.ResponseModel != "routed-model" || result.RawStopReason != "stop" {
			b.Fatal(result)
		}
	}
}
