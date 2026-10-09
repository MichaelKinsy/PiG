package agent

import (
	"reflect"
	"testing"
	"time"
)

// upstream: packages/ai/src/utils/event-stream.ts:41-70 push/end: events pushed before end are delivered in order, end resolves the result, and a push after end is ignored.
func TestAgentEventStream_EndResolvesTheResultAndStopsDelivery(t *testing.T) {
	stream := newAgentEventStream()
	first, second := AgentStartEvent{}, TurnStartEvent{}
	stream.Push(first)
	stream.Push(second)
	messages := []AgentMessage{{User: &UserMessage{Role: "user", Timestamp: 1}}}
	stream.End(messages)
	stream.Push(AgentStartEvent{})
	stream.End([]AgentMessage{{User: &UserMessage{Role: "user", Timestamp: 2}}})

	var got []AgentEvent
	for event := range stream.Events() {
		got = append(got, event)
	}
	if want := []AgentEvent{first, second}; !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %#v, want %#v (the push after End must be ignored)", got, want)
	}
	result := stream.Result()
	if !reflect.DeepEqual(result, messages) {
		t.Fatalf("result = %#v; want the first End's messages", result)
	}
}

// upstream: packages/agent/src/agent-loop.ts:153-157 createAgentStream: agent_end is the completing event, so pushing it resolves the
// result with its messages and ends delivery (event-stream.ts:44-48), without a separate end().
func TestAgentEventStream_PushedAgentEndCompletesTheStream(t *testing.T) {
	stream := newAgentEventStream()
	messages := []AgentMessage{{User: &UserMessage{Role: "user", Timestamp: 3}}}
	end := AgentEndEvent{Messages: messages}
	stream.Push(AgentStartEvent{})
	stream.Push(end)
	stream.Push(TurnStartEvent{})

	var got []AgentEvent
	done := make(chan struct{})
	go func() {
		defer close(done)
		for event := range stream.Events() {
			got = append(got, event)
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Events did not end after agent_end was pushed")
	}
	if want := []AgentEvent{AgentStartEvent{}, end}; !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %#v, want %#v (agent_end completes the stream)", got, want)
	}
	result := stream.Result()
	if !reflect.DeepEqual(result, messages) {
		t.Fatalf("result = %#v; want the agent_end messages", result)
	}
}
