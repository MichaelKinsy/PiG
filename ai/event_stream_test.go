package ai

import (
	"context"
	"encoding/json"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
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
	synctest.Wait()
	stream.mu.Lock()
	got := len(stream.waiters)
	stream.mu.Unlock()
	if got != count {
		t.Fatalf("stream waiter count = %d, want %d", got, count)
	}
}

// runAsExecutorProducer runs body as the stream's producer turn, as a managed provider's response body does.
func runAsExecutorProducer(stream *AssistantMessageEventStream, body func()) {
	stream.mu.Lock()
	if stream.executor == nil {
		stream.executor = &continuationExecutor{}
	}
	stream.mu.Unlock()
	_ = stream.responseContinuation(func(*continuationTurn) error {
		body()
		return nil
	})
}

// runManagedBuilder runs body as a managed builder's producer turn.
func runManagedBuilder(builder *assistantStreamBuilder, body func()) {
	builder.managed = true
	runAsExecutorProducer(builder.stream, func() {
		builder.stream.mu.Lock()
		builder.turn = builder.stream.producer
		builder.stream.mu.Unlock()
		defer func() { builder.turn = nil }()
		body()
	})
}

// Pi event-stream.ts:44-91 queues references; iteration after Result observes finalized state, not the producer's push-time state.
func TestAssistantStreamBuilderQueuedPartialsObserveFinalState(t *testing.T) {
	builder := newAssistantStreamBuilder(context.Background(), APIOpenAIResponses, "test", "model")
	runManagedBuilder(builder, func() {
		builder.textDelta("answer")
		builder.done(StopReasonStop, nil, "")
	})
	builder.stream.Result()

	var events []AssistantMessageEvent
	for event := range builder.stream.Events(context.Background()) {
		events = append(events, event)
	}
	start := events[0].(StartEvent).Partial.Observe()
	if len(start.Content) != 1 || start.Content[0].(TextContent).Text != "answer" || start.StopReason != StopReasonStop {
		t.Fatalf("queued start retained emission state: %#v", start)
	}
	delta := events[2].(TextDeltaEvent).Partial.Observe()
	if len(delta.Content) != 1 || delta.Content[0].(TextContent).Text != "answer" || delta.StopReason != StopReasonStop {
		t.Fatalf("queued delta retained emission state: %#v", delta)
	}
	done := events[len(events)-1].(DoneEvent)
	if done.Message != builder.stream.Result() {
		t.Fatal("terminal Message pointer identity changed")
	}
}

// Pi retains the delivered reference across an awaited result; a separately captured observation stays owned data.
func TestAssistantStreamBuilderDeliveredPartialAdvancesAfterResult(t *testing.T) {
	builder := newAssistantStreamBuilder(t.Context(), APIOpenAIResponses, "test", "model")
	runManagedBuilder(builder, builder.start)
	var retained *AssistantMessage
	for event := range builder.stream.Events(t.Context()) {
		retained = event.(StartEvent).Partial
		break
	}
	before := retained.Observe()
	runManagedBuilder(builder, func() {
		builder.textDelta("later")
		builder.done(StopReasonStop, nil, "")
	})
	builder.stream.Result()
	after := retained.Observe()
	if len(before.Content) != 0 || before.StopReason != StopReasonPending {
		t.Fatalf("owned observation changed: %#v", before)
	}
	if len(after.Content) != 1 || after.Content[0].(TextContent).Text != "later" || after.StopReason != StopReasonStop {
		t.Fatalf("retained view did not advance: %#v", after)
	}
}

// A producer outside the executor cannot be ordered against its consumer, so every nonterminal partial is its emission-time snapshot. Pi has no such producer; this keeps the observation deterministic (D82).
func TestAssistantStreamBuilderUnmanagedProducerPublishesEmissionSnapshots(t *testing.T) {
	for run := range 50 {
		builder := newAssistantStreamBuilder(t.Context(), APIAnthropicMessages, "test", "model")
		produced := make(chan struct{})
		go func() {
			defer close(produced)
			builder.start()
			builder.textDelta("one")
			builder.textDelta(" two")
			builder.done(StopReasonStop, nil, "")
		}()
		if run%2 == 0 {
			<-produced
		}
		var observed []string
		for event := range builder.stream.Events(t.Context()) {
			var partial *AssistantMessage
			switch event := event.(type) {
			case StartEvent:
				partial = event.Partial
			case TextDeltaEvent:
				partial = event.Partial
			default:
				continue
			}
			data, err := json.Marshal(partial.Observe())
			if err != nil {
				t.Fatal(err)
			}
			var fields struct {
				Content    []map[string]any `json:"content"`
				StopReason StopReason       `json:"stopReason"`
			}
			if err := json.Unmarshal(data, &fields); err != nil {
				t.Fatal(err)
			}
			text := ""
			if len(fields.Content) > 0 {
				text, _ = fields.Content[0]["text"].(string)
			}
			observed = append(observed, string(fields.StopReason)+":"+text)
		}
		<-produced
		if want := []string{"pending:", "pending:one", "pending:one two"}; !reflect.DeepEqual(observed, want) {
			t.Fatalf("run %d observed %q, want emission snapshots %q", run, observed, want)
		}
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

// Pi's EventStream.push completes on done/error without imposing a start event (utils/event-stream.ts:44-63).
func TestAssistantMessageEventStreamDoneCanTerminateBeforeStart(t *testing.T) {
	stream := NewAssistantMessageEventStream()
	message := testAssistant(StopReasonStop)
	if err := stream.Push(DoneEvent{Reason: StopReasonStop, Message: message}); err != nil {
		t.Fatal(err)
	}
	if stream.Result() != message {
		t.Fatal("terminal pointer changed")
	}
	var events []AssistantMessageEvent
	for event := range stream.Events(t.Context()) {
		events = append(events, event)
	}
	if len(events) != 1 {
		t.Fatalf("synthetic events: %#v", events)
	}
	if done, ok := events[0].(DoneEvent); !ok || done.Message != message {
		t.Fatalf("terminal event: %#v", events[0])
	}
}

// Pi event-stream.ts:44-63 does not invent start-order restrictions for valid typed events. Agent decides which pre-start updates it observes.
func TestAssistantMessageEventStreamAcceptsTypedEventOrder(t *testing.T) {
	stream := NewAssistantMessageEventStream()
	partial := testAssistant(StopReasonPending)
	for _, event := range []AssistantMessageEvent{
		TextDeltaEvent{Delta: "early", Partial: partial},
		StartEvent{Partial: partial},
		StartEvent{Partial: partial},
	} {
		if err := stream.Push(event); err != nil {
			t.Fatalf("typed %s rejected by an invented sequence rule: %v", event.EventType(), err)
		}
	}
	stream.End(testAssistant(StopReasonStop))
	var types []AssistantEventType
	for event := range stream.Events(t.Context()) {
		types = append(types, event.EventType())
	}
	if want := []AssistantEventType{EventTextDelta, EventStart, EventStart}; !reflect.DeepEqual(types, want) {
		t.Fatalf("event order=%v, want %v", types, want)
	}
}

func TestAssistantMessageEventStreamRejectsInvalidPayloads(t *testing.T) {
	stream := NewAssistantMessageEventStream()
	if err := stream.Push(StartEvent{}); err == nil {
		t.Fatal("missing partial succeeded")
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
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		stream := NewAssistantMessageEventStream()
		defer stream.End()
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
	})
}

func TestAssistantMessageEventStreamTerminalWakesAllWaitingConsumers(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		stream := NewAssistantMessageEventStream()
		defer stream.End()
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
	})
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
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		stream := NewAssistantMessageEventStream()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		finished := make(chan struct{})
		go func() {
			defer close(finished)
			for range stream.Events(ctx) {
			}
		}()
		waitForStreamWaiters(t, stream, 1)
		cancel()
		synctest.Wait()
		select {
		case <-finished:
		default:
			t.Fatal("canceled iterator did not return")
		}
		stream.mu.Lock()
		defer stream.mu.Unlock()
		if len(stream.waiters) != 0 {
			t.Fatalf("canceled iterator retained %d waiters", len(stream.waiters))
		}
	})
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
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		stream := NewAssistantMessageEventStream()
		defer stream.End()
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
	})
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
