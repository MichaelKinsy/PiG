package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"
)

type tokensUpstreamCase struct {
	name, provider, model string
	api                   API
	oauth                 bool
	effort                string
	googleThinking        bool
}

func abortUsageClass(m *GeneratedModel) int {
	if m.API == APIOpenAICompletions || m.API == APIMistralConversations || m.API == APIOpenAIResponses || m.API == APIAzureOpenAIResponses || m.API == APIOpenAICodexResponses || m.Provider == "zai" || m.Provider == "amazon-bedrock" || m.Provider == "vercel-ai-gateway" || m.Provider == "minimax" {
		return 0
	}
	if m.Provider == "kimi-coding" {
		return 1
	}
	return 2
}

func TestTokensOnAbortUpstream(t *testing.T) {
	for _, tc := range tokensUpstreamCases() {
		t.Run(tc.provider+"/"+tc.model+"/"+tc.name, func(t *testing.T) {
			var generated *GeneratedModel
			if tc.model == "preferred" {
				models := ListModels("cerebras")
				if len(models) == 0 {
					t.Fatal("No Cerebras models available")
				}
				generated = &models[0]
				for i := range models {
					if slices.Contains([]string{"gpt-oss-120b", "zai-glm-4.7", "llama3.1-8b"}, models[i].ID) {
						generated = &models[i]
						break
					}
				}
			} else {
				var ok bool
				generated, ok = LookupModelExact(tc.provider + "/" + tc.model)
				if !ok {
					t.Fatal("missing upstream model")
				}
			}
			model := *generated
			if tc.api != "" {
				model.API = tc.api
				model.Compat = nil
			}
			closed := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(closed)
				body, err := decodeMatrixRequest(r)
				if err != nil {
					t.Error(err)
					return
				}
				if !jsonContainsText(body, "Write a long poem with 20 stanzas about the beauty of nature.") {
					t.Error("missing original prompt")
				}
				writeAbortPrefix(t, w, &model)
				w.(http.Flusher).Flush()
				// The response cannot finish before cancellation is observed by the transport.
				<-r.Context().Done()
			}))
			t.Cleanup(server.Close)
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			t.Cleanup(cancel)
			provider := newMatrixProvider(t, &model, server.URL, tc.oauth)
			t.Cleanup(func() {
				if err := provider.Close(); err != nil {
					t.Error(err)
				}
			})
			options := StreamOptions{IsReasoning: model.Reasoning, ModelCost: model.ToModel().CostRates(), ReasoningEffort: tc.effort, Transport: TransportSSE, Env: ProviderEnv{"AWS_BEDROCK_SKIP_AUTH": "1", "AWS_REGION": "us-east-1"}}
			if tc.googleThinking {
				options.GoogleThinking = &GoogleThinkingOptions{Enabled: true}
			}
			stream, err := provider.Stream(ctx, NormalizeContext(Context{SystemPrompt: "You are a helpful assistant.", Messages: []Message{UserMessage{Content: UserText("Write a long poem with 20 stanzas about the beauty of nature."), Timestamp: 1}}}), options)
			if err != nil {
				t.Fatal(err)
			}
			aborted := false
			text := ""
			for event := range stream.Events(t.Context()) {
				var delta string
				switch event := event.(type) {
				case TextDeltaEvent:
					delta = event.Delta
				case ThinkingDeltaEvent:
					delta = event.Delta
				}
				if !aborted {
					text += delta
					if len(utf16.Encode([]rune(text))) >= 1000 {
						aborted = true
						cancel()
					}
				}
			}
			response := stream.Result()
			if !aborted || response.StopReason != StopReasonAborted {
				t.Fatalf("abort fired=%t response=%+v", aborted, response)
			}
			usage := response.Usage
			switch abortUsageClass(&model) {
			case 0:
				if usage.Input != 0 || usage.Output != 0 {
					t.Fatalf("late-only usage = %+v", usage)
				}
			case 1:
				if usage.Input <= 0 || usage.Output != 0 {
					t.Fatalf("input-only usage = %+v", usage)
				}
			case 2:
				if usage.Input <= 0 || usage.Output <= 0 {
					t.Fatalf("early usage = %+v", usage)
				}
				if model.InputCostPerMTokens > 0 && (usage.Cost.Input <= 0 || usage.Cost.Total <= 0) {
					t.Fatalf("early costs = %+v", usage.Cost)
				}
			}
			<-closed
		})
	}
}

func writeAbortPrefix(t *testing.T, w http.ResponseWriter, m *GeneratedModel) {
	t.Helper()
	text := strings.Repeat("nature 🙈\n", 125)
	input, output := 0, 0
	if abortUsageClass(m) > 0 {
		input = 100
	}
	if abortUsageClass(m) > 1 {
		output = 1
	}
	write := func(format string, args ...any) {
		if _, err := fmt.Fprintf(w, format, args...); err != nil {
			t.Error(err)
		}
	}
	sse := func(value any) {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Error(err)
			return
		}
		write("data: %s\n\n", raw)
	}
	w.Header().Set("Content-Type", "text/event-stream")
	switch m.API {
	case APIAnthropicMessages:
		for _, event := range []map[string]any{
			{"type": "message_start", "message": map[string]any{"id": "msg_abort", "usage": map[string]any{"input_tokens": input, "output_tokens": output}}},
			{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}},
			{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": text}},
		} {
			write("event: %s\n", event["type"])
			sse(event)
		}
	case APIOpenAICompletions, APIMistralConversations:
		sse(map[string]any{"id": "chatcmpl_abort", "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": text}, "finish_reason": nil}}})
	case APIOpenAIResponses, APIAzureOpenAIResponses, APIOpenAICodexResponses:
		sse(map[string]any{"type": "response.output_item.added", "output_index": 0, "item": map[string]any{"type": "message", "id": "msg_abort", "content": []any{}}})
		sse(map[string]any{"type": "response.output_text.delta", "output_index": 0, "delta": text})
	case APIGoogleGenerativeAI:
		sse(map[string]any{"usageMetadata": map[string]any{"promptTokenCount": input, "candidatesTokenCount": output, "totalTokenCount": input + output}})
		sse(map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"role": "model", "parts": []any{map[string]any{"text": text}}}}}})
	case APIBedrockConverseStream:
		w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
		encoder := eventstream.NewEncoder()
		for _, event := range []struct {
			name string
			body any
		}{{"messageStart", map[string]any{"role": "assistant"}}, {"contentBlockDelta", map[string]any{"contentBlockIndex": 0, "delta": map[string]any{"text": text}}}} {
			raw, err := json.Marshal(event.body)
			if err != nil {
				t.Error(err)
				return
			}
			headers := eventstream.Headers{}
			headers.Set(":message-type", eventstream.StringValue("event"))
			headers.Set(":event-type", eventstream.StringValue(event.name))
			headers.Set(":content-type", eventstream.StringValue("application/json"))
			if err := encoder.Encode(w, eventstream.Message{Headers: headers, Payload: raw}); err != nil {
				t.Error(err)
			}
		}
	default:
		t.Fatalf("unsupported abort API %s", m.API)
	}
}
