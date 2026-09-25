package ai

import (
	"context"
	"encoding/json"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testAssistant(stop StopReason) *AssistantMessage {
	return &AssistantMessage{Content: []AssistantContentBlock{}, API: APIOpenAIResponses, Provider: "test", Model: "model/name", Usage: Usage{}, StopReason: stop, Timestamp: 123}
}

func pushTestStart(t *testing.T, stream *AssistantMessageEventStream) *AssistantMessage {
	t.Helper()
	partial := testAssistant(StopReasonPending)
	if err := stream.Push(StartEvent{Partial: partial}); err != nil {
		t.Fatal(err)
	}
	return partial
}

func pushTestDone(t *testing.T, stream *AssistantMessageEventStream) *AssistantMessage {
	t.Helper()
	final := testAssistant(StopReasonStop)
	if err := stream.Push(DoneEvent{Reason: StopReasonStop, Message: final}); err != nil {
		t.Fatal(err)
	}
	return final
}

func waitForStreamWaiters(t *testing.T, stream *AssistantMessageEventStream, count int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		stream.mu.Lock()
		got := len(stream.waiters)
		stream.mu.Unlock()
		if got == count {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("stream waiter count did not reach %d", count)
}

func TestAssistantStreamBuilderNonterminalEventsRetainEmissionState(t *testing.T) {
	builder := newAssistantStreamBuilder(context.Background(), APIOpenAIResponses, "test", "model")
	builder.textDelta("answer")
	builder.done(StopReasonStop, nil, "")

	var events []AssistantMessageEvent
	for event := range builder.stream.Events(context.Background()) {
		events = append(events, event)
	}
	start := events[0].(StartEvent)
	if len(start.Partial.Content) != 0 || start.Partial.StopReason != StopReasonPending {
		t.Fatalf("start partial changed after emission: %#v", start.Partial)
	}
	delta := events[2].(TextDeltaEvent)
	if len(delta.Partial.Content) != 1 || delta.Partial.Content[0].(TextContent).Text != "answer" || delta.Partial.StopReason != StopReasonPending {
		t.Fatalf("delta partial changed after emission: %#v", delta.Partial)
	}
	done := events[len(events)-1].(DoneEvent)
	if done.Message != builder.stream.Result() {
		t.Fatal("terminal Message pointer identity changed")
	}
}

func TestAssistantMessageEventStreamResultIsTerminalMessagePointerWithoutIteration(t *testing.T) {
	stream := NewAssistantMessageEventStream()
	partial := pushTestStart(t, stream)
	final := testAssistant(StopReasonStop)
	for i := range 1000 {
		partial.Content = []AssistantContentBlock{TextContent{Text: string(rune('a' + i%26))}}
		if err := stream.Push(TextDeltaEvent{ContentIndex: 0, Delta: "x", Partial: partial}); err != nil {
			t.Fatal(err)
		}
	}
	if err := stream.Push(DoneEvent{Reason: StopReasonStop, Message: final}); err != nil {
		t.Fatal(err)
	}
	if result := stream.Result(); result != final {
		t.Fatalf("Result() = %p, want %p", result, final)
	}

	events := []AssistantMessageEvent{}
	for event := range stream.Events(context.Background()) {
		events = append(events, event)
	}
	done, ok := events[len(events)-1].(DoneEvent)
	if !ok || done.Message != final || len(events) != 1002 {
		t.Fatalf("terminal events = %d %#v", len(events), events[len(events)-1])
	}
}

func TestAssistantMessageEventStreamErrorCanTerminateBeforeStart(t *testing.T) {
	stream := NewAssistantMessageEventStream()
	final := testAssistant(StopReasonError)
	final.ErrorMessage = "auth failed"
	if err := stream.Push(ErrorEvent{Reason: StopReasonError, Error: final}); err != nil {
		t.Fatal(err)
	}
	if result := stream.Result(); result != final {
		t.Fatalf("Result() = %p, want %p", result, final)
	}
	var events []AssistantMessageEvent
	for event := range stream.Events(context.Background()) {
		events = append(events, event)
	}
	if len(events) != 1 {
		t.Fatalf("event count = %d, want 1", len(events))
	}
	terminal, ok := events[0].(ErrorEvent)
	if !ok || terminal.Error != final {
		t.Fatalf("terminal event = %#v", events[0])
	}
}

func TestAssistantMessageEventStreamRejectsInvalidOrdering(t *testing.T) {
	stream := NewAssistantMessageEventStream()
	partial := testAssistant(StopReasonPending)
	if err := stream.Push(TextDeltaEvent{Delta: "early", Partial: partial}); err == nil {
		t.Fatal("delta before start succeeded")
	}
	if err := stream.Push(StartEvent{Partial: partial}); err != nil {
		t.Fatal(err)
	}
	if err := stream.Push(StartEvent{Partial: partial}); err == nil {
		t.Fatal("second start succeeded")
	}
	if err := stream.Push(DoneEvent{Reason: StopReasonError, Message: testAssistant(StopReasonError)}); err == nil {
		t.Fatal("invalid done reason succeeded")
	}
}

func TestAssistantMessageEventStreamDrainsBufferedEventsAndIgnoresLatePushes(t *testing.T) {
	stream := NewAssistantMessageEventStream()
	partial := pushTestStart(t, stream)
	if err := stream.Push(TextDeltaEvent{Delta: "one", Partial: partial}); err != nil {
		t.Fatal(err)
	}
	final := pushTestDone(t, stream)
	if err := stream.Push(TextDeltaEvent{Delta: "late", Partial: partial}); err != nil {
		t.Fatalf("late push = %v, want ignored", err)
	}
	if stream.Result() != final {
		t.Fatal("result does not share the terminal message pointer")
	}
	var types []AssistantEventType
	for event := range stream.Events(context.Background()) {
		types = append(types, event.EventType())
	}
	want := []AssistantEventType{EventStart, EventTextDelta, EventDone}
	if !reflect.DeepEqual(types, want) {
		t.Fatalf("event types = %v, want %v", types, want)
	}
}

func TestAssistantMessageEventStreamPreservesOrderWhileDraining(t *testing.T) {
	stream := NewAssistantMessageEventStream()
	partial := pushTestStart(t, stream)
	if err := stream.Push(TextDeltaEvent{Delta: "one", Partial: partial}); err != nil {
		t.Fatal(err)
	}

	got := make(chan AssistantEventType, 4)
	allowSecond := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		for event := range stream.Events(context.Background()) {
			got <- event.EventType()
			if event.EventType() == EventStart {
				<-allowSecond
			}
		}
	}()
	if eventType := <-got; eventType != EventStart {
		t.Fatalf("first event = %s, want %s", eventType, EventStart)
	}
	if err := stream.Push(TextDeltaEvent{Delta: "two", Partial: partial}); err != nil {
		t.Fatal(err)
	}
	pushTestDone(t, stream)
	close(allowSecond)
	<-finished
	close(got)
	var remaining []AssistantEventType
	for eventType := range got {
		remaining = append(remaining, eventType)
	}
	want := []AssistantEventType{EventTextDelta, EventTextDelta, EventDone}
	if !reflect.DeepEqual(remaining, want) {
		t.Fatalf("remaining event types = %v, want %v", remaining, want)
	}
}

func TestAssistantMessageEventStreamDeliversToWaitingConsumersInRegistrationOrder(t *testing.T) {
	stream := NewAssistantMessageEventStream()
	first := make(chan AssistantEventType, 1)
	second := make(chan AssistantEventType, 1)
	go func() {
		for event := range stream.Events(context.Background()) {
			first <- event.EventType()
			return
		}
	}()
	waitForStreamWaiters(t, stream, 1)
	go func() {
		for event := range stream.Events(context.Background()) {
			second <- event.EventType()
			return
		}
	}()
	waitForStreamWaiters(t, stream, 2)

	partial := testAssistant(StopReasonPending)
	if err := stream.Push(StartEvent{Partial: partial}); err != nil {
		t.Fatal(err)
	}
	if err := stream.Push(TextDeltaEvent{Delta: "x", Partial: partial}); err != nil {
		t.Fatal(err)
	}
	if got := <-first; got != EventStart {
		t.Fatalf("first waiter received %s, want %s", got, EventStart)
	}
	if got := <-second; got != EventTextDelta {
		t.Fatalf("second waiter received %s, want %s", got, EventTextDelta)
	}
	pushTestDone(t, stream)
}

func TestAssistantMessageEventStreamTerminalWakesAllWaitingConsumers(t *testing.T) {
	stream := NewAssistantMessageEventStream()
	partial := testAssistant(StopReasonPending)
	if err := stream.Push(StartEvent{Partial: partial}); err != nil {
		t.Fatal(err)
	}
	for range stream.Events(context.Background()) {
		break
	}

	var wg sync.WaitGroup
	wg.Add(2)
	counts := make(chan int, 2)
	for range 2 {
		go func() {
			defer wg.Done()
			count := 0
			for range stream.Events(context.Background()) {
				count++
			}
			counts <- count
		}()
	}
	waitForStreamWaiters(t, stream, 2)
	pushTestDone(t, stream)
	wg.Wait()
	close(counts)
	var got []int
	for count := range counts {
		got = append(got, count)
	}
	if len(got) != 2 || got[0]+got[1] != 1 {
		t.Fatalf("waiter delivery counts = %v, want one terminal delivery total", got)
	}
}

func TestAssistantMessageEventStreamAbandonedIterationLeavesNoWaiterOrDeliveryGoroutine(t *testing.T) {
	stream := NewAssistantMessageEventStream()
	pushTestStart(t, stream)
	for range stream.Events(context.Background()) {
		break
	}
	stream.mu.Lock()
	waiters := len(stream.waiters)
	queued := len(stream.queue)
	stream.mu.Unlock()
	if waiters != 0 || queued != 0 {
		t.Fatalf("after abandoned iteration: waiters=%d queue=%d, want zero", waiters, queued)
	}
	pushTestDone(t, stream)
	if result := stream.Result(); result == nil {
		t.Fatal("Result() is nil after abandoned iteration")
	}
}

func TestAssistantMessageEventStreamReleasesConsumedQueueEntries(t *testing.T) {
	stream := NewAssistantMessageEventStream()
	pushTestStart(t, stream)
	pushTestDone(t, stream)
	for range stream.Events(context.Background()) {
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if stream.queue != nil {
		t.Fatalf("consumed queue retained %d entries", len(stream.queue))
	}
}

func TestAssistantMessageEventStreamCancellationRemovesWaitingIterator(t *testing.T) {
	stream := NewAssistantMessageEventStream()
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		for range stream.Events(ctx) {
		}
	}()
	waitForStreamWaiters(t, stream, 1)
	cancel()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("canceled iterator did not return")
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if len(stream.waiters) != 0 {
		t.Fatalf("canceled iterator retained %d waiters", len(stream.waiters))
	}
}

func TestAssistantMessageEventStreamAssignedWaiterWinsCancellationResolution(t *testing.T) {
	stream := NewAssistantMessageEventStream()
	waiter := &eventWaiter{ready: make(chan eventDelivery, 1)}
	stream.waiters = append(stream.waiters, waiter)
	partial := testAssistant(StopReasonPending)
	if err := stream.Push(StartEvent{Partial: partial}); err != nil {
		t.Fatal(err)
	}

	delivery, assigned := stream.cancelWaiter(waiter)
	if !assigned {
		t.Fatal("cancelWaiter reported an assigned waiter as canceled")
	}
	if eventType := delivery.event.EventType(); eventType != EventStart {
		t.Fatalf("assigned event = %s, want %s", eventType, EventStart)
	}
}

func TestAssistantMessageEventStreamAssignmentWinsCancellationWithoutReordering(t *testing.T) {
	stream := NewAssistantMessageEventStream()
	firstContext, cancelFirst := context.WithCancel(context.Background())
	defer cancelFirst()
	first := make(chan AssistantEventType, 1)
	second := make(chan AssistantEventType, 1)
	go func() {
		for event := range stream.Events(firstContext) {
			first <- event.EventType()
			return
		}
	}()
	waitForStreamWaiters(t, stream, 1)
	go func() {
		for event := range stream.Events(context.Background()) {
			second <- event.EventType()
			return
		}
	}()
	waitForStreamWaiters(t, stream, 2)

	partial := testAssistant(StopReasonPending)
	if err := stream.Push(StartEvent{Partial: partial}); err != nil {
		t.Fatal(err)
	}
	waitForStreamWaiters(t, stream, 1)
	cancelFirst()
	if err := stream.Push(TextDeltaEvent{ContentIndex: 0, Delta: "second", Partial: partial}); err != nil {
		t.Fatal(err)
	}
	if got := <-first; got != EventStart {
		t.Fatalf("assigned canceled waiter received %s, want %s", got, EventStart)
	}
	if got := <-second; got != EventTextDelta {
		t.Fatalf("second waiter received %s, want %s", got, EventTextDelta)
	}
	pushTestDone(t, stream)
}

func TestAssistantMessageEventStreamConcurrentPushResultAndIterators(t *testing.T) {
	stream := NewAssistantMessageEventStream()
	partial := pushTestStart(t, stream)
	var consumed atomic.Int64
	var consumers sync.WaitGroup
	consumers.Add(2)
	for range 2 {
		go func() {
			defer consumers.Done()
			for range stream.Events(context.Background()) {
				consumed.Add(1)
			}
		}()
	}

	var producers sync.WaitGroup
	for range 100 {
		producers.Go(func() {
			if err := stream.Push(TextDeltaEvent{ContentIndex: 0, Delta: "x", Partial: partial}); err != nil {
				t.Errorf("Push() = %v", err)
			}
		})
	}
	resultReady := make(chan *AssistantMessage, 1)
	go func() { resultReady <- stream.Result() }()
	producers.Wait()
	final := pushTestDone(t, stream)
	if result := <-resultReady; result != final {
		t.Fatalf("Result() = %p, want %p", result, final)
	}
	consumers.Wait()
	if got := consumed.Load(); got != 102 {
		t.Fatalf("consumed events = %d, want 102", got)
	}
}

func TestAssistantMessageEventJSONHasOnlyVariantFields(t *testing.T) {
	partial := testAssistant(StopReasonPending)
	encoded, err := json.Marshal(TextDeltaEvent{ContentIndex: 2, Delta: "x", Partial: partial})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	if got := string(fields["type"]); got != `"text_delta"` {
		t.Fatalf("event type = %s, want text_delta", got)
	}
	for _, required := range []string{"type", "contentIndex", "delta", "partial"} {
		if _, exists := fields[required]; !exists {
			t.Errorf("TextDeltaEvent missing %s: %s", required, encoded)
		}
	}
	for _, invalid := range []string{"message", "error", "reason", "thinking", "tool_call", "usage"} {
		if _, exists := fields[invalid]; exists {
			t.Errorf("TextDeltaEvent contains %s: %s", invalid, encoded)
		}
	}
}
