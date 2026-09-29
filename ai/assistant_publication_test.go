package ai

import (
	"encoding/json"
	"reflect"
	"runtime"
	"sync"
	"testing"
)

func TestAssistantMessagePublicationPreservesConstructionIdentity(t *testing.T) {
	stream := NewAssistantMessageEventStream()
	first := testAssistant(StopReasonPending)
	first.Content = []AssistantContentBlock{TextContent{Text: "first"}}
	second := testAssistant(StopReasonPending)
	second.Content = []AssistantContentBlock{TextContent{Text: "second"}}
	runAsExecutorProducer(stream, func() {
		if err := stream.Push(StartEvent{Partial: first}); err != nil {
			t.Fatal(err)
		}
		if err := stream.Push(TextDeltaEvent{Partial: second, ContentIndex: 0, Delta: "second"}); err != nil {
			t.Fatal(err)
		}
	})
	var views []*AssistantMessage
	for event := range stream.Events(t.Context()) {
		switch event := event.(type) {
		case StartEvent:
			views = append(views, event.Partial)
		case TextDeltaEvent:
			views = append(views, event.Partial)
		}
		if len(views) == 2 {
			break
		}
	}
	first.Content[0] = TextContent{Text: "advanced"}
	first.StopReason = StopReasonStop
	stream.End(first)
	if got := views[0].Observe(); got.Content[0].(TextContent).Text != "advanced" || got.StopReason != StopReasonStop {
		t.Fatalf("original reference did not advance: %#v", got)
	}
	if got := views[1].Observe(); got.Content[0].(TextContent).Text != "second" || got.StopReason != StopReasonPending {
		t.Fatalf("a different construction object acquired the final state: %#v", got)
	}
	if stream.Result() != first {
		t.Fatal("End result identity changed")
	}
	stream.mu.Lock()
	if stream.publications != nil {
		t.Error("terminal stream retained the source identity registry")
	}
	stream.mu.Unlock()
	runtime.KeepAlive(second)
}

func TestAssistantMessagePublicationDoesNotMutateImmutableInput(t *testing.T) {
	stream := NewAssistantMessageEventStream()
	partial := testAssistant(StopReasonPending)
	partial.Content = []AssistantContentBlock{TextContent{Text: "unchanged"}}
	before := cloneAssistantMessage(*partial)
	if err := stream.Push(StartEvent{Partial: partial}); err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for range 4 {
		workers.Go(func() {
			for range 64 {
				if _, err := json.Marshal(partial); err != nil {
					t.Error(err)
				}
				if err := stream.Push(TextDeltaEvent{ContentIndex: 0, Delta: "x", Partial: partial}); err != nil {
					t.Error(err)
				}
			}
		})
	}
	workers.Wait()
	if !reflect.DeepEqual(*partial, before) {
		t.Fatal("publishing added mutable metadata to the caller's message")
	}
	stream.End(testAssistant(StopReasonStop))
	for event := range stream.Events(t.Context()) {
		if delta, ok := event.(TextDeltaEvent); ok && delta.Partial.Observe().Content[0].(TextContent).Text != "unchanged" {
			t.Error("publication changed immutable input data")
		}
	}
}
