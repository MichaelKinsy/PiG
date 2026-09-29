package ai

import (
	"fmt"
	"reflect"
	"sync"
	"testing"
)

// packages/ai/src/api/anthropic-messages.ts:596 emits start before reading message_start, so its snapshot has no provider response ID or usage.
func TestAnthropicSSEStartPrecedesProviderMetadata(t *testing.T) {
	_, events := runAnthropicWire(t, AnthropicConfig{Model: "claude-haiku-4-5", APIKey: "fake-key"}, Context{Messages: []Message{UserMessage{Content: UserText("Hello"), Timestamp: 1}}}, StreamOptions{}, func(map[string]any) string { return anthropicMinimalFixture() })
	if len(events) == 0 {
		t.Fatal("no stream events")
	}
	start, ok := events[0].(StartEvent)
	if !ok {
		t.Fatalf("first event is %T, want start", events[0])
	}
	if start.Partial.ResponseID != "" || !reflect.DeepEqual(start.Partial.Usage, Usage{}) || len(start.Partial.Content) != 0 {
		t.Fatalf("start already contains provider metadata: responseId=%q usage=%+v content=%#v", start.Partial.ResponseID, start.Partial.Usage, start.Partial.Content)
	}
}

// packages/ai/src/api/anthropic-messages.ts:595-596: the provider pushes a reference to `output` as start after the response is observed and before it reads the body, and a consumer reads that shared message at its own tick. A consumer that attaches only after start is queued, but before any body data, therefore delivers a start without response ID, usage or content; the same message, refreshed after delivery, is the live object Pi's consumer would read at a later tick.
func TestAnthropicSSEStartPrecedesProviderMetadataWhenConsumerAttachesAfterStart(t *testing.T) {
	release := make(chan struct{})
	_, baseURL := serveAnthropicWire(t, func(map[string]any) string { return anthropicMinimalFixture() }, release)
	unblock := sync.OnceFunc(func() { close(release) })
	t.Cleanup(unblock) // runs before the server's Close, so a failed test never leaves the handler waiting
	ctx, executor := withContinuationExecutor(t.Context())
	started := make(chan struct{})
	executor.trace = &executorTrace{push: func(_ *AssistantMessageEventStream, event AssistantMessageEvent) {
		if _, ok := event.(StartEvent); ok {
			close(started)
		}
	}}
	stream, err := NewAnthropicProvider(AnthropicConfig{Model: "claude-haiku-4-5", APIKey: "fake-key", BaseURL: baseURL}).Stream(ctx, NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("Hello"), Timestamp: 1}}}), StreamOptions{})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	select {
	case <-started:
	case <-stream.done:
		t.Fatalf("stream ended before start: %#v", stream.result)
	}
	var events []AssistantMessageEvent
	for event := range stream.Events(t.Context()) {
		events = append(events, event)
		unblock()
	}
	start, ok := events[0].(StartEvent)
	if !ok {
		t.Fatalf("first event is %T, want start", events[0])
	}
	if start.Partial.ResponseID != "" || !reflect.DeepEqual(start.Partial.Usage, Usage{}) || len(start.Partial.Content) != 0 {
		t.Fatalf("start already contains provider metadata: responseId=%q usage=%+v content=%#v", start.Partial.ResponseID, start.Partial.Usage, start.Partial.Content)
	}
	done, ok := events[len(events)-1].(DoneEvent)
	if !ok || done.Message.ResponseID != "msg_test" {
		t.Fatalf("last event = %#v, want done carrying the response ID released after start", events[len(events)-1])
	}
	if refreshed := RefreshEvent(start).(StartEvent).Partial; refreshed.ResponseID != "msg_test" {
		t.Fatalf("refreshed start responseId = %q, want the shared message's later state", refreshed.ResponseID)
	}
}

// packages/ai/src/api/anthropic-messages.ts:640-727 emits start/delta/end events for the addressed block without prematurely ending another open block.
func TestAnthropicSSEIndexedEventOrder(t *testing.T) {
	events := anthropicFixtureEvent("message_start", `{"message":{"id":"msg_index_events","usage":{"input_tokens":12,"output_tokens":0}}}`) +
		anthropicFixtureEvent("content_block_start", `{"index":3,"content_block":{"type":"text","text":"first"}}`) +
		anthropicFixtureEvent("content_block_start", `{"index":7,"content_block":{"type":"text","text":"second"}}`) +
		anthropicFixtureEvent("content_block_delta", `{"index":3,"delta":{"type":"text_delta","text":" A"}}`) +
		anthropicFixtureEvent("content_block_delta", `{"index":7,"delta":{"type":"text_delta","text":" B"}}`) +
		anthropicFixtureEvent("content_block_stop", `{"index":7}`) + anthropicFixtureEvent("content_block_stop", `{"index":3}`) + anthropicFixtureEnd("end_turn")
	_, emitted := runAnthropicWire(t, AnthropicConfig{Model: "claude-haiku-4-5", APIKey: "fake-key"}, Context{Messages: []Message{UserMessage{Content: UserText("Hello"), Timestamp: 1}}}, StreamOptions{}, func(map[string]any) string { return events })
	var trace []string
	for _, event := range emitted {
		switch event := event.(type) {
		case TextStartEvent:
			trace = append(trace, fmt.Sprintf("text_start:%d:%s", event.ContentIndex, event.Partial.Content[event.ContentIndex].(TextContent).Text))
		case TextDeltaEvent:
			trace = append(trace, fmt.Sprintf("text_delta:%d:%s", event.ContentIndex, event.Delta))
		case TextEndEvent:
			trace = append(trace, fmt.Sprintf("text_end:%d:%s", event.ContentIndex, event.Content))
		default:
			trace = append(trace, string(event.EventType()))
		}
	}
	want := []string{"start", "text_start:0:first", "text_start:1:second", "text_delta:0: A", "text_delta:1: B", "text_end:1:second B", "text_end:0:first A", "done"}
	if !reflect.DeepEqual(trace, want) {
		t.Fatalf("events=%q; want %q", trace, want)
	}
}

// packages/ai/src/api/anthropic-messages.ts:640-727 dispatches deltas by both the provider index and the stored content type, not a global active block.
func TestAnthropicSSEIndexedBlocks(t *testing.T) {
	for _, tc := range []struct {
		name   string
		events string
		want   []AssistantContentBlock
	}{
		{"overlapping text blocks",
			anthropicFixtureEvent("content_block_start", `{"index":3,"content_block":{"type":"text","text":"first"}}`) +
				anthropicFixtureEvent("content_block_start", `{"index":7,"content_block":{"type":"text","text":"second"}}`) +
				anthropicFixtureEvent("content_block_delta", `{"index":3,"delta":{"type":"text_delta","text":" A"}}`) +
				anthropicFixtureEvent("content_block_delta", `{"index":7,"delta":{"type":"text_delta","text":" B"}}`) +
				anthropicFixtureEvent("content_block_stop", `{"index":7}`) + anthropicFixtureEvent("content_block_stop", `{"index":3}`),
			[]AssistantContentBlock{TextContent{Text: "first A"}, TextContent{Text: "second B"}}},
		{"overlapping thinking blocks",
			anthropicFixtureEvent("content_block_start", `{"index":3,"content_block":{"type":"thinking","thinking":"first","signature":"sig1"}}`) +
				anthropicFixtureEvent("content_block_start", `{"index":7,"content_block":{"type":"thinking","thinking":"second","signature":"sig2"}}`) +
				anthropicFixtureEvent("content_block_delta", `{"index":3,"delta":{"type":"thinking_delta","thinking":" A"}}`) +
				anthropicFixtureEvent("content_block_delta", `{"index":7,"delta":{"type":"signature_delta","signature":" B"}}`) +
				anthropicFixtureEvent("content_block_stop", `{"index":7}`) + anthropicFixtureEvent("content_block_stop", `{"index":3}`),
			[]AssistantContentBlock{ThinkingContent{Thinking: "first A", ThinkingSignature: "sig1"}, ThinkingContent{Thinking: "second", ThinkingSignature: "sig2 B"}}},
		{"text delta on thinking is ignored",
			anthropicFixtureEvent("content_block_start", `{"index":3,"content_block":{"type":"thinking","thinking":"kept","signature":"sig"}}`) +
				anthropicFixtureEvent("content_block_delta", `{"index":3,"delta":{"type":"text_delta","text":"not text"}}`) + anthropicFixtureEvent("content_block_stop", `{"index":3}`),
			[]AssistantContentBlock{ThinkingContent{Thinking: "kept", ThinkingSignature: "sig"}}},
		{"thinking delta on text is ignored",
			anthropicFixtureEvent("content_block_start", `{"index":3,"content_block":{"type":"text","text":"kept"}}`) +
				anthropicFixtureEvent("content_block_delta", `{"index":3,"delta":{"type":"thinking_delta","thinking":"not thinking"}}`) + anthropicFixtureEvent("content_block_stop", `{"index":3}`),
			[]AssistantContentBlock{TextContent{Text: "kept"}}},
		{"tool delta on text is ignored",
			anthropicFixtureEvent("content_block_start", `{"index":3,"content_block":{"type":"text","text":"kept"}}`) +
				anthropicFixtureEvent("content_block_delta", `{"index":3,"delta":{"type":"input_json_delta","partial_json":"{}"}}`) + anthropicFixtureEvent("content_block_stop", `{"index":3}`),
			[]AssistantContentBlock{TextContent{Text: "kept"}}},
		{"unknown and closed indexes are ignored",
			anthropicFixtureEvent("content_block_start", `{"index":3,"content_block":{"type":"text","text":"kept"}}`) +
				anthropicFixtureEvent("content_block_delta", `{"index":99,"delta":{"type":"text_delta","text":"unknown"}}`) + anthropicFixtureEvent("content_block_stop", `{"index":3}`) +
				anthropicFixtureEvent("content_block_delta", `{"index":3,"delta":{"type":"text_delta","text":"closed"}}`),
			[]AssistantContentBlock{TextContent{Text: "kept"}}},
		{"thinking signature after text starts",
			anthropicFixtureEvent("content_block_start", `{"index":3,"content_block":{"type":"thinking","thinking":"kept","signature":"sig"}}`) +
				anthropicFixtureEvent("content_block_start", `{"index":7,"content_block":{"type":"text","text":"hello"}}`) +
				anthropicFixtureEvent("content_block_delta", `{"index":3,"delta":{"type":"signature_delta","signature":" tail"}}`) + anthropicFixtureEvent("content_block_stop", `{"index":3}`) + anthropicFixtureEvent("content_block_stop", `{"index":7}`),
			[]AssistantContentBlock{ThinkingContent{Thinking: "kept", ThinkingSignature: "sig tail"}, TextContent{Text: "hello"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			events := anthropicFixtureEvent("message_start", `{"message":{"id":"msg_index","usage":{"input_tokens":12,"output_tokens":0}}}`) + tc.events + anthropicFixtureEnd("end_turn")
			result, _, _ := streamAnthropicFixture(t, AnthropicConfig{Model: "claude-haiku-4-5"}, events, StreamOptions{})
			if result.StopReason != StopReasonStop || result.ErrorMessage != "" {
				t.Fatalf("result=%+v", result)
			}
			if !reflect.DeepEqual(result.Content, tc.want) {
				t.Fatalf("content=%#v; want %#v", result.Content, tc.want)
			}
		})
	}
}
