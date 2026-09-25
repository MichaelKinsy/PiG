package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestModelEventStreamPreservesOrderAndResult(t *testing.T) {
	stream := newModelEventStream()
	stream.push(map[string]any{"type": "start"})
	stream.push(map[string]any{"type": "text_delta", "delta": "ok"})
	terminal := map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "ok"}}}
	stream.push(map[string]any{"type": "done", "message": terminal})
	var types []string
	for event := range stream.Events(context.Background()) {
		types = append(types, event["type"].(string))
	}
	if !reflect.DeepEqual(types, []string{"start", "text_delta", "done"}) {
		t.Fatalf("types = %v", types)
	}
	if !reflect.DeepEqual(stream.Result(), terminal) {
		t.Fatalf("result = %#v", stream.Result())
	}
}

func TestModelEventStreamResultDoesNotDependOnAbandonedIterator(t *testing.T) {
	stream := newModelEventStream()
	stream.push(map[string]any{"type": "start"})
	terminal := map[string]any{"stopReason": "stop"}
	stream.push(map[string]any{"type": "done", "message": terminal})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = stream.Events(ctx)
	time.Sleep(20 * time.Millisecond)
	result := make(chan map[string]any, 1)
	go func() { result <- stream.Result() }()
	select {
	case got := <-result:
		if !reflect.DeepEqual(got, terminal) {
			t.Fatalf("result = %#v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("Result blocked on an abandoned iterator")
	}
}

func TestModelEventStreamCancellationLeavesUnassignedEventForAnotherConsumer(t *testing.T) {
	stream := newModelEventStream()
	stream.push(map[string]any{"type": "start", "sequence": 1})
	stream.push(map[string]any{"type": "done", "message": map[string]any{"stopReason": "stop"}})
	ctx, cancel := context.WithCancel(context.Background())
	cancelledEvents := stream.Events(ctx)
	time.Sleep(20 * time.Millisecond)
	cancel()
	if events := collectSDKModelEvents(cancelledEvents); len(events) != 0 {
		t.Fatalf("cancelled consumer received %#v", events)
	}
	events := collectSDKModelEvents(stream.Events(context.Background()))
	if len(events) != 2 || events[0]["sequence"] != 1 || events[1]["type"] != "done" {
		t.Fatalf("remaining events = %#v", events)
	}
}

func TestModelEventStreamCompetingConsumersShareFIFOWithoutRetention(t *testing.T) {
	stream := newModelEventStream()
	for sequence := 0; sequence < 1000; sequence++ {
		stream.push(map[string]any{"type": "text_delta", "sequence": sequence})
	}
	stream.push(map[string]any{"type": "done", "message": map[string]any{"stopReason": "stop"}})
	var mu sync.Mutex
	seen := map[int]int{}
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for _, event := range collectSDKModelEvents(stream.Events(context.Background())) {
				if sequence, ok := event["sequence"].(int); ok {
					mu.Lock()
					seen[sequence]++
					mu.Unlock()
				}
			}
		}()
	}
	wait.Wait()
	for sequence := 0; sequence < 1000; sequence++ {
		if seen[sequence] != 1 {
			t.Fatalf("sequence %d deliveries = %d", sequence, seen[sequence])
		}
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if stream.queue != nil {
		t.Fatalf("drained queue retains backing storage: len=%d cap=%d", len(stream.queue), cap(stream.queue))
	}
}

func collectSDKModelEvents(events <-chan map[string]any) []map[string]any {
	var collected []map[string]any
	for event := range events {
		collected = append(collected, event)
	}
	return collected
}

func TestModelStreamTransportErrorShape(t *testing.T) {
	event := modelStreamErrorEvent(errors.New("transport boom"), map[string]any{"api": "openai-responses", "provider": "conformance", "modelId": "transport-error"})
	errorMessage := event["error"].(map[string]any)
	usage := errorMessage["usage"].(map[string]any)
	cost := usage["cost"].(map[string]any)
	if errorMessage["role"] != "assistant" || errorMessage["api"] != "openai-responses" || errorMessage["provider"] != "conformance" || errorMessage["model"] != "transport-error" || errorMessage["stopReason"] != "error" || errorMessage["errorMessage"] != "transport boom" || errorMessage["timestamp"].(int64) <= 0 || usage["totalTokens"] != 0 || cost["total"] != 0 {
		t.Fatalf("event = %#v", event)
	}
}

func TestExtensionRoutesModelStreamNotify(t *testing.T) {
	extension := New("test")
	stream := newModelEventStream()
	extension.modelStreams["stream-1"] = stream
	args, err := json.Marshal(map[string]any{"streamId": "stream-1", "event": map[string]any{"type": "done", "message": map[string]any{"stopReason": "stop"}}})
	if err != nil {
		t.Fatal(err)
	}
	extension.handleNotify(envelope{Notify: &notifyMsg{Method: "model_stream_event", Args: args}})
	if stream.Result()["stopReason"] != "stop" {
		t.Fatalf("result = %#v", stream.Result())
	}
}
