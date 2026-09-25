package ai

import (
	"context"
	"reflect"
	"testing"
)

func collectBuilderEvents(stream *AssistantMessageEventStream) []AssistantMessageEvent {
	var events []AssistantMessageEvent
	for event := range stream.Events(context.Background()) {
		events = append(events, event)
	}
	return events
}

func TestAssistantStreamBuilderEndsConcurrentToolCallsInContentOrder(t *testing.T) {
	for attempt := range 100 {
		builder := newAssistantStreamBuilder(context.Background(), APIOpenAICompletions, "test", "model")
		for _, index := range []int{2, 0, 1} {
			builder.toolCallDelta(streamToolCallDelta{index: index, id: string(rune('a' + index)), name: "tool", argumentsDelta: `{}`})
		}
		builder.done(StopReasonToolUse, nil, "")
		var got []int
		for _, event := range collectBuilderEvents(builder.stream) {
			if end, ok := event.(ToolCallEndEvent); ok {
				got = append(got, end.ContentIndex)
			}
		}
		if !reflect.DeepEqual(got, []int{0, 1, 2}) {
			t.Fatalf("attempt %d tool-call end order = %v, want [0 1 2]", attempt, got)
		}
	}
}

func TestParseStreamingJsonObjectMatchesPiPartialCases(t *testing.T) {
	for _, test := range []struct {
		raw  string
		want JsonObject
	}{
		{`{"a":1,`, JsonObject{"a": float64(1)}},
		{`{"a":`, JsonObject{}},
		{`{"a":[1,2`, JsonObject{"a": []any{float64(1), float64(2)}}},
		{`{"a": tru`, JsonObject{"a": true}},
		{"{\"a\":\"x\\q", JsonObject{"a": "x"}},
	} {
		if got := parseStreamingJsonObject(test.raw); !reflect.DeepEqual(got, test.want) {
			t.Errorf("parseStreamingJsonObject(%q) = %#v, want %#v", test.raw, got, test.want)
		}
	}
}

func TestAssistantStreamBuilderRepairsPartialToolArgumentsAndKeepsTerminalConsistent(t *testing.T) {
	for _, test := range []struct {
		name string
		raw  string
		want JsonObject
	}{
		{name: "repairable", raw: `{"path":"partial`, want: JsonObject{"path": "partial"}},
		{name: "irreparable", raw: `{nonsense`, want: JsonObject{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			builder := newAssistantStreamBuilder(context.Background(), APIOpenAICompletions, "test", "model")
			builder.toolCallDelta(streamToolCallDelta{index: 0, id: "call", name: "read", argumentsDelta: test.raw})
			builder.done(StopReasonToolUse, nil, "")

			events := collectBuilderEvents(builder.stream)
			if len(events) != 5 {
				t.Fatalf("events = %#v, want start/tool-start/tool-delta/tool-end/done", events)
			}
			end, ok := events[3].(ToolCallEndEvent)
			if !ok || !reflect.DeepEqual(end.ToolCall.Arguments, test.want) {
				t.Fatalf("tool end = %#v, want arguments %#v", events[3], test.want)
			}
			done, ok := events[4].(DoneEvent)
			if !ok || done.Reason != StopReasonToolUse || done.Message.StopReason != done.Reason {
				t.Fatalf("terminal event = %#v", events[4])
			}
			if builder.stream.Result() != done.Message {
				t.Fatalf("result=%p done=%p", builder.stream.Result(), done.Message)
			}
		})
	}
}
