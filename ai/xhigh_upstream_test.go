package ai

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The endpoint acts as the provider's effort validator; it accepts ordinary effort but rejects xhigh on gpt-5-mini. This keeps the upstream response assertions hermetic without accepting clamped request values.
func TestXHighReasoningUpstream(t *testing.T) {
	for _, tc := range []struct {
		name, id string
		api      API
		success  bool
	}{
		// .upstream/v0.87.1/packages/ai/test/xhigh.test.ts:20
		{"gpt 5.5 (supports xhigh)/should work with openai-responses", "gpt-5.5", APIOpenAIResponses, true},
		// .upstream/v0.87.1/packages/ai/test/xhigh.test.ts:39
		{"gpt-5-mini (does not support xhigh)/should error with openai-responses when using xhigh", "gpt-5-mini", APIOpenAIResponses, false},
		// .upstream/v0.87.1/packages/ai/test/xhigh.test.ts:52
		{"gpt-5-mini (does not support xhigh)/should error with openai-completions when using xhigh", "gpt-5-mini", APIOpenAICompletions, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			captured := make(chan string, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				}
				effort, _ := body["reasoning_effort"].(string)
				if reasoning, ok := body["reasoning"].(map[string]any); ok {
					effort, _ = reasoning["effort"].(string)
				}
				captured <- effort
				if effort == "xhigh" && !tc.success {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusBadRequest)
					_, _ = io.WriteString(w, `{"error":{"message":"Unsupported value: xhigh","type":"invalid_request_error"}}`)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				if tc.api == APIOpenAICompletions {
					_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"40\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
					return
				}
				if effort == "xhigh" {
					_, _ = io.WriteString(w, "data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"type\":\"reasoning\",\"id\":\"rs_1\",\"summary\":[]}}\n\ndata: {\"type\":\"response.reasoning_summary_text.delta\",\"output_index\":0,\"delta\":\"17 + 23 is 40\"}\n\ndata: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"type\":\"reasoning\",\"id\":\"rs_1\",\"summary\":[{\"text\":\"17 + 23 is 40\"}]}}\n\n")
				}
				_, _ = io.WriteString(w, "data: {\"type\":\"response.output_item.added\",\"output_index\":1,\"item\":{\"type\":\"message\",\"id\":\"msg_1\",\"content\":[]}}\n\ndata: {\"type\":\"response.output_text.delta\",\"output_index\":1,\"delta\":\"40\"}\n\ndata: {\"type\":\"response.output_item.done\",\"output_index\":1,\"item\":{\"type\":\"message\",\"id\":\"msg_1\",\"content\":[{\"type\":\"output_text\",\"text\":\"40\"}]}}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
			}))
			defer server.Close()
			var provider Provider
			if tc.api == APIOpenAIResponses {
				provider = NewOpenAIResponsesProvider(OpenAIResponsesConfig{BaseURL: server.URL, APIKey: "test", Model: tc.id, ProviderID: "openai", IsReasoning: true})
			} else {
				provider = NewOpenAIProvider(OpenAIConfig{BaseURL: server.URL, APIKey: "test", Model: tc.id, ProviderID: "openai", Compat: &OpenAICompat{SupportsReasoningEffort: new(true)}})
			}
			defer func() {
				if err := provider.Close(); err != nil {
					t.Error(err)
				}
			}()
			options := StreamOptions{IsReasoning: true}
			// The raw API option name is deliberately decoded as extensions supply it.
			if err := json.Unmarshal([]byte(`{"reasoningEffort":"xhigh"}`), &options); err != nil {
				t.Fatal(err)
			}
			stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("What is 17 + 23? Think step by step."), Timestamp: 1}}}), options)
			if err != nil {
				// Provider setup errors are returned in Go; ModelRuntime converts them to the upstream terminal error message (also covered by the paired caller fixture).
				if tc.success || !strings.Contains(err.Error(), "xhigh") {
					t.Fatal(err)
				}
				if effort := <-captured; effort != "xhigh" {
					t.Fatalf("raw reasoning effort = %q; want xhigh without clamping", effort)
				}
				return
			}
			hasThinking := false
			for event := range stream.Events(t.Context()) {
				switch event.(type) {
				case ThinkingStartEvent, ThinkingDeltaEvent:
					hasThinking = true
				}
			}
			response := stream.Result()
			if tc.success {
				if response.StopReason != StopReasonStop {
					t.Fatalf("stopReason = %s; error = %s", response.StopReason, response.ErrorMessage)
				}
				text, thinking := false, false
				for _, block := range response.Content {
					switch block.(type) {
					case TextContent:
						text = true
					case ThinkingContent:
						thinking = true
					}
				}
				if !text || (!hasThinking && !thinking) {
					t.Fatalf("text=%t thinking=%t streamed thinking=%t", text, thinking, hasThinking)
				}
			} else if response.StopReason != StopReasonError || !strings.Contains(response.ErrorMessage, "xhigh") {
				t.Fatalf("response = %+v", response)
			}
			if effort := <-captured; effort != "xhigh" {
				t.Fatalf("raw reasoning effort = %q; want xhigh without clamping", effort)
			}
		})
	}
}
