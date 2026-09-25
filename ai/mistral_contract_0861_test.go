package ai

import (
	"context"
	"io"
	"reflect"
	"strings"
	"testing"
)

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
