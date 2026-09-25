package ai

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func runAnthropicEvents(t *testing.T, events string) (*AssistantMessage, []AssistantMessageEvent) {
	t.Helper()
	provider := &anthropicProvider{}
	builder := newAssistantStreamBuilder(context.Background(), APIAnthropicMessages, "anthropic", "model")
	go provider.parseAnthropicSSE(context.Background(), strings.NewReader(events), builder, anthropicStreamNames{})
	result := builder.stream.Result()
	var got []AssistantMessageEvent
	for event := range builder.stream.Events(context.Background()) {
		got = append(got, event)
	}
	return result, got
}

func TestAnthropicStreamPreservesBlocksUsageAndMetadata(t *testing.T) {
	events := `event: message_start
data: {"message":{"id":"message-1","usage":{"input_tokens":10,"output_tokens":0,"cache_read_input_tokens":2,"cache_creation_input_tokens":3,"cache_creation":{"ephemeral_1h_input_tokens":1},"output_tokens_details":{"thinking_tokens":4}}}}

event: content_block_start
data: {"index":0,"content_block":{"type":"thinking"}}

event: content_block_delta
data: {"index":0,"delta":{"type":"thinking_delta","thinking":"reason"}}

event: content_block_delta
data: {"index":0,"delta":{"type":"signature_delta","signature":"signature"}}

event: content_block_stop
data: {"index":0}

event: content_block_start
data: {"index":1,"content_block":{"type":"text"}}

event: content_block_delta
data: {"index":1,"delta":{"type":"text_delta","text":"answer"}}

event: content_block_stop
data: {"index":1}

event: content_block_start
data: {"index":2,"content_block":{"type":"tool_use","id":"call-1","name":"read"}}

event: content_block_delta
data: {"index":2,"delta":{"type":"input_json_delta","partial_json":"{\"path\":\"main.go\"}"}}

event: content_block_stop
data: {"index":2}

event: message_delta
data: {"delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":5}}

event: message_stop
data: {}

`
	result, eventsOut := runAnthropicEvents(t, events)
	wantContent := []AssistantContentBlock{
		ThinkingContent{Thinking: "reason", ThinkingSignature: "signature"},
		TextContent{Text: "answer"},
		ToolCall{ID: "call-1", Name: "read", Arguments: JsonObject{"path": "main.go"}},
	}
	if !reflect.DeepEqual(result.Content, wantContent) {
		t.Fatalf("content = %#v, want %#v", result.Content, wantContent)
	}
	if result.ResponseID != "message-1" || result.RawStopReason != "tool_use" || result.StopReason != StopReasonToolUse {
		t.Fatalf("metadata = response %q raw %q stop %q", result.ResponseID, result.RawStopReason, result.StopReason)
	}
	if result.Usage.Input != 10 || result.Usage.Output != 5 || result.Usage.CacheRead != 2 || result.Usage.CacheWrite != 3 || result.Usage.CacheWrite1h == nil || *result.Usage.CacheWrite1h != 1 || result.Usage.Reasoning == nil || *result.Usage.Reasoning != 4 || result.Usage.TotalTokens != 20 {
		t.Fatalf("usage = %#v", result.Usage)
	}
	wantTypes := []AssistantEventType{
		EventStart,
		EventThinkingStart, EventThinkingDelta, EventThinkingEnd,
		EventTextStart, EventTextDelta, EventTextEnd,
		EventToolCallStart, EventToolCallDelta, EventToolCallEnd,
		EventDone,
	}
	var types []AssistantEventType
	for _, event := range eventsOut {
		types = append(types, event.EventType())
	}
	if !reflect.DeepEqual(types, wantTypes) {
		t.Fatalf("event types = %v, want %v", types, wantTypes)
	}
}

func TestAnthropicStopReasonMapping(t *testing.T) {
	cases := []struct {
		reason      string
		want        StopReason
		wantMessage string
	}{
		{reason: "end_turn", want: StopReasonStop},
		{reason: "max_tokens", want: StopReasonLength},
		{reason: "tool_use", want: StopReasonToolUse},
		{reason: "pause_turn", want: StopReasonStop},
		{reason: "stop_sequence", want: StopReasonStop},
		{reason: "refusal", want: StopReasonError, wantMessage: "The model refused to complete the request"},
		{reason: "sensitive", want: StopReasonError, wantMessage: "Provider stopped with: sensitive"},
		{reason: "future_reason", want: StopReasonError, wantMessage: "Unhandled stop reason: future_reason"},
	}
	for _, test := range cases {
		t.Run(test.reason, func(t *testing.T) {
			events := "event: message_start\n" +
				"data: {\"message\":{\"id\":\"message-1\",\"usage\":{\"input_tokens\":7}}}\n\n" +
				"event: message_delta\n" +
				fmt.Sprintf("data: {\"delta\":{\"stop_reason\":%q},\"usage\":{\"output_tokens\":2}}\n\n", test.reason) +
				"event: message_stop\n" +
				"data: {}\n\n"
			result, _ := runAnthropicEvents(t, events)
			if result.StopReason != test.want || result.RawStopReason != test.reason || result.ErrorMessage != test.wantMessage {
				t.Fatalf("result = reason %q raw %q error %q; want %q %q %q", result.StopReason, result.RawStopReason, result.ErrorMessage, test.want, test.reason, test.wantMessage)
			}
			wantOutput, wantTotal := 2, 9
			if test.reason == "future_reason" {
				// mapStopReason throws before the same delta's usage is applied.
				wantOutput, wantTotal = 0, 7
			}
			if result.Usage.Input != 7 || result.Usage.Output != wantOutput || result.Usage.TotalTokens != wantTotal {
				t.Fatalf("usage = %#v, want input 7 output %d total %d", result.Usage, wantOutput, wantTotal)
			}
		})
	}
}

func TestAnthropicStreamWithoutStopReasonTerminatesWithError(t *testing.T) {
	cases := map[string]string{
		"message_stop without message_delta": "event: message_start\n" +
			"data: {\"message\":{\"id\":\"message-1\",\"usage\":{}}}\n\n" +
			"event: message_stop\n" +
			"data: {}\n\n",
		"empty stream": "",
	}
	for name, events := range cases {
		t.Run(name, func(t *testing.T) {
			result, got := runAnthropicEvents(t, events)
			if result.StopReason != StopReasonError || result.ErrorMessage != "Anthropic stream ended without a stop reason" {
				t.Fatalf("result = reason %q error %q", result.StopReason, result.ErrorMessage)
			}
			if got[len(got)-1].EventType() != EventError {
				t.Fatalf("terminal event = %s", got[len(got)-1].EventType())
			}
		})
	}
}

func TestAnthropicStreamWithoutMessageStopTerminatesWithError(t *testing.T) {
	events := `event: message_start
data: {"message":{"id":"message-1","usage":{}}}

event: content_block_start
data: {"index":0,"content_block":{"type":"text"}}

event: content_block_delta
data: {"index":0,"delta":{"type":"text_delta","text":"partial"}}

`
	result, got := runAnthropicEvents(t, events)
	if result.StopReason != StopReasonError || result.ErrorMessage != "Anthropic stream ended before message_stop" {
		t.Fatalf("result = %#v", result)
	}
	if got[len(got)-1].EventType() != EventError {
		t.Fatalf("terminal event = %s", got[len(got)-1].EventType())
	}
}
