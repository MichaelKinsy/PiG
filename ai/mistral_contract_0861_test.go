//go:build !pig_strip_mistral_conversations

package ai

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// Ports packages/ai/test/mistral-raw-stop-reason.test.ts:39,51,63 through HTTP dispatch and SSE consumption.
func TestMistralRawStopReasonsUpstream(t *testing.T) {
	for _, tc := range []struct {
		raw     string
		stop    StopReason
		message string
	}{
		{"stop", StopReasonStop, ""},
		{"error", StopReasonError, "Provider stopped with: error (server error)"},
		{"unmapped_error", StopReasonError, "Provider stopped with: unmapped_error"},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			model := mustGeneratedModel(t, "mistral", "devstral-medium-latest")
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprintf(w, "data: {\"id\":\"mistral-response-id\",\"model\":%q,\"choices\":[{\"index\":0,\"finish_reason\":%q,\"delta\":{}}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":0,\"total_tokens\":1}}\n\ndata: [DONE]\n\n", model.ID, tc.raw)
			}))
			t.Cleanup(server.Close)
			provider := NewMistralProvider(MistralConfig{APIKey: "test", Model: model.ID, BaseURL: server.URL})
			stream, err := provider.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hello")}}}), StreamOptions{})
			if err != nil {
				t.Fatal(err)
			}
			message := stream.Result()
			if message.StopReason != tc.stop || message.RawStopReason != tc.raw || message.ErrorMessage != tc.message {
				t.Fatalf("message = %#v", message)
			}
			// upstream: mistral-raw-stop-reason.test.ts:55-70 (#10487): a finish_reason "error" is retryable, an unknown reason is not.
			if got, want := IsRetryableAssistantError(*message), tc.raw == "error"; got != want {
				t.Fatalf("IsRetryableAssistantError = %v, want %v for %q", got, want, tc.message)
			}
		})
	}
}

func TestMistralStreamPreservesResponseAndCachedUsageMetadata(t *testing.T) {
	stream := NewAssistantMessageEventStream()
	builder := newAssistantStreamBuilder(context.Background(), APIMistralConversations, "mistral", "model")
	body := io.NopCloser(strings.NewReader("data: {\"id\":\"resp-1\",\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":20,\"total_tokens\":120,\"prompt_tokens_details\":{\"cached_tokens\":40}}}\n\ndata: [DONE]\n\n"))
	builder.stream = stream
	provider := &mistralProvider{}
	provider.consumeStream(context.Background(), body, builder)
	result := stream.Result()
	if result.ResponseID != "resp-1" {
		t.Fatalf("responseId = %q, want resp-1", result.ResponseID)
	}
	if result.RawStopReason != "stop" {
		t.Fatalf("rawStopReason = %q, want stop", result.RawStopReason)
	}
	if result.Usage.Input != 60 || result.Usage.CacheRead != 40 || result.Usage.Output != 20 || result.Usage.TotalTokens != 120 {
		t.Fatalf("usage = %#v", result.Usage)
	}
}

func TestMistralStreamWithoutFinishReasonTerminatesWithError(t *testing.T) {
	stream := NewAssistantMessageEventStream()
	builder := newAssistantStreamBuilder(context.Background(), APIMistralConversations, "mistral", "model")
	builder.stream = stream
	body := io.NopCloser(strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\ndata: [DONE]\n\n"))
	provider := &mistralProvider{}
	provider.consumeStream(context.Background(), body, builder)
	result := stream.Result()
	if result.StopReason != StopReasonError || result.ErrorMessage != "Mistral stream ended without a finish reason" {
		t.Fatalf("terminal result = %#v", result)
	}
}

func TestMistralAlternatingContentAndToolCallsCloseEachBlockOnce(t *testing.T) {
	body := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":[{"type":"text","text":"first"}]}}]}`,
		`data: {"choices":[{"delta":{"content":[{"type":"thinking","thinking":[{"type":"text","text":"reason"}]}]}}]}`,
		`data: {"choices":[{"delta":{"content":[{"type":"text","text":"second"}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call-1","function":{"name":"read","arguments":"{\"path\":\"main.go\"}"}}]},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
		"",
	}, "\n\n")
	stream := NewAssistantMessageEventStream()
	builder := newAssistantStreamBuilder(context.Background(), APIMistralConversations, "mistral", "model")
	builder.stream = stream
	provider := &mistralProvider{}
	provider.consumeStream(context.Background(), io.NopCloser(strings.NewReader(body)), builder)
	result := stream.Result()
	if result.StopReason != StopReasonToolUse || len(result.Content) != 4 {
		t.Fatalf("result = %#v", result)
	}
	wantContent := []AssistantContentBlock{
		TextContent{Text: "first"},
		ThinkingContent{Thinking: "reason"},
		TextContent{Text: "second"},
		ToolCall{ID: "call-1", Name: "read", Arguments: JsonObject{"path": "main.go"}},
	}
	if !reflect.DeepEqual(result.Content, wantContent) {
		t.Fatalf("content = %#v, want %#v", result.Content, wantContent)
	}
	var types []AssistantEventType
	for event := range stream.Events(context.Background()) {
		types = append(types, event.EventType())
	}
	wantTypes := []AssistantEventType{
		EventStart,
		EventTextStart, EventTextDelta, EventTextEnd,
		EventThinkingStart, EventThinkingDelta, EventThinkingEnd,
		EventTextStart, EventTextDelta, EventTextEnd,
		EventToolCallStart, EventToolCallDelta, EventToolCallEnd,
		EventDone,
	}
	if !reflect.DeepEqual(types, wantTypes) {
		t.Fatalf("event types = %v, want %v", types, wantTypes)
	}
}

func TestMistralUnknownFinishReasonTerminatesWithProviderMessage(t *testing.T) {
	stream := NewAssistantMessageEventStream()
	builder := newAssistantStreamBuilder(context.Background(), APIMistralConversations, "mistral", "model")
	builder.stream = stream
	body := io.NopCloser(strings.NewReader("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"future_reason\"}]}\n\ndata: [DONE]\n\n"))
	provider := &mistralProvider{}
	provider.consumeStream(context.Background(), body, builder)
	result := stream.Result()
	if result.StopReason != StopReasonError || result.RawStopReason != "future_reason" || result.ErrorMessage != "Provider stopped with: future_reason" {
		t.Fatalf("terminal result = %#v", result)
	}
}
