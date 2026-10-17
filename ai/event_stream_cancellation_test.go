package ai

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// A context canceled while its consumer handles an event (no next() pending) still ends the iteration: events already queued are delivered, and the next wait ends instead of waiting for an event.
func TestIteratorCanceledBetweenEventsEndsAtTheNextWait(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		stream := NewAssistantMessageEventStream()
		defer stream.End()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		partial := testAssistant(StopReasonPending)
		var received []AssistantEventType
		handling := make(chan struct{})
		resume := make(chan struct{})
		finished := make(chan struct{})
		go func() {
			defer close(finished)
			for event := range stream.Events(ctx) {
				received = append(received, event.EventType())
				if event.EventType() == EventStart {
					close(handling)
					<-resume
				}
			}
		}()
		waitForStreamWaiters(t, stream, 1)
		if err := stream.Push(StartEvent{Partial: partial}); err != nil {
			t.Fatal(err)
		}
		<-handling
		cancel()
		synctest.Wait()
		if err := stream.Push(TextDeltaEvent{ContentIndex: 0, Delta: "queued", Partial: partial}); err != nil {
			t.Fatal(err)
		}
		close(resume)
		synctest.Wait()
		select {
		case <-finished:
		default:
			t.Fatal("an iteration canceled between events kept waiting")
		}
		if len(received) != 2 || received[0] != EventStart || received[1] != EventTextDelta {
			t.Fatalf("received %v, want [start text_delta]", received)
		}
		stream.mu.Lock()
		defer stream.mu.Unlock()
		if len(stream.waiters) != 0 {
			t.Fatalf("canceled iteration retained %d waiters", len(stream.waiters))
		}
	})
}

// registrationContext is a cancelable context whose context.AfterFunc registrations are counted: registered counts every registration, live those neither stopped nor run.
type registrationContext struct {
	mu         sync.Mutex
	done       chan struct{}
	err        error
	registered int
	live       map[*func()]bool
	values     map[any]any
}

func newRegistrationContext() *registrationContext {
	return &registrationContext{done: make(chan struct{}), live: map[*func()]bool{}}
}

func (c *registrationContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c *registrationContext) Done() <-chan struct{}       { return c.done }
func (c *registrationContext) Value(key any) any           { return c.values[key] }

func (c *registrationContext) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

func (c *registrationContext) AfterFunc(f func()) func() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.registered++
	key := &f
	c.live[key] = true
	return func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		stopped := c.live[key]
		delete(c.live, key)
		return stopped
	}
}

func (c *registrationContext) cancel() {
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return
	}
	c.err = context.Canceled
	close(c.done)
	var run []func()
	for key := range c.live {
		run = append(run, *key)
	}
	clear(c.live)
	c.mu.Unlock()
	for _, f := range run {
		go f()
	}
}

func (c *registrationContext) counts() (registered, live int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.registered, len(c.live)
}

// One iteration on a cancelable context registers its cancellation once, however many times it waits, and releases the registration when it ends.
func TestIterationRegistersItsCancellationOnce(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		const events = 64
		stream := NewAssistantMessageEventStream()
		ctx := newRegistrationContext()
		defer ctx.cancel()
		partial := testAssistant(StopReasonPending)
		delivered := 0
		finished := make(chan struct{})
		go func() {
			defer close(finished)
			for range stream.Events(ctx) {
				delivered++
			}
		}()
		waitForStreamWaiters(t, stream, 1)
		if err := stream.Push(StartEvent{Partial: partial}); err != nil {
			t.Fatal(err)
		}
		for range events {
			waitForStreamWaiters(t, stream, 1)
			if err := stream.Push(TextDeltaEvent{ContentIndex: 0, Delta: "d", Partial: partial}); err != nil {
				t.Fatal(err)
			}
		}
		waitForStreamWaiters(t, stream, 1)
		pushTestDone(t, stream)
		<-finished
		if delivered != events+2 {
			t.Fatalf("delivered %d events, want %d", delivered, events+2)
		}
		if registered, live := ctx.counts(); registered != 1 || live != 0 {
			t.Fatalf("the iteration made %d cancellation registrations and left %d live, want 1 and 0", registered, live)
		}
	})
}

// Canceling the counted context while the iteration waits ends it through the shared registration.
func TestIterationSharedCancellationEndsAWaitingIteration(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		stream := NewAssistantMessageEventStream()
		defer stream.End()
		ctx := newRegistrationContext()
		partial := testAssistant(StopReasonPending)
		finished := make(chan struct{})
		go func() {
			defer close(finished)
			for range stream.Events(ctx) {
			}
		}()
		waitForStreamWaiters(t, stream, 1)
		if err := stream.Push(StartEvent{Partial: partial}); err != nil {
			t.Fatal(err)
		}
		waitForStreamWaiters(t, stream, 1)
		ctx.cancel()
		synctest.Wait()
		select {
		case <-finished:
		default:
			t.Fatal("canceled iteration did not return")
		}
		stream.mu.Lock()
		defer stream.mu.Unlock()
		if len(stream.waiters) != 0 {
			t.Fatalf("canceled iteration retained %d waiters", len(stream.waiters))
		}
	})
}

// A continuation-turn iteration (the forwarders' events loop) on a cancelable context also registers its cancellation once and releases it when it ends.
func TestTurnIterationRegistersItsCancellationOnce(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		const events = 64
		stream := NewAssistantMessageEventStream()
		executor := &continuationExecutor{}
		ctx := newRegistrationContext()
		defer ctx.cancel()
		partial := testAssistant(StopReasonPending)
		go func() {
			waitForStreamWaiters(t, stream, 1)
			if err := stream.Push(StartEvent{Partial: partial}); err != nil {
				t.Error(err)
			}
			for range events {
				waitForStreamWaiters(t, stream, 1)
				if err := stream.Push(TextDeltaEvent{ContentIndex: 0, Delta: "d", Partial: partial}); err != nil {
					t.Error(err)
				}
			}
			waitForStreamWaiters(t, stream, 1)
			pushTestDone(t, stream)
		}()
		delivered := 0
		executor.run(func(turn *continuationTurn) {
			ctx.values = map[any]any{continuationExecutorKey{}: executor, continuationTurnKey{}: turn}
			for range stream.events(ctx, false) {
				delivered++
			}
		})
		if delivered != events+2 {
			t.Fatalf("delivered %d events, want %d", delivered, events+2)
		}
		if registered, live := ctx.counts(); registered != 1 || live != 0 {
			t.Fatalf("the iteration made %d cancellation registrations and left %d live, want 1 and 0", registered, live)
		}
	})
}

// uncomparableContext is a cancelable context passed by value whose dynamic type Go cannot compare with ==, as a context struct carrying a map or a slice is.
type uncomparableContext struct {
	context.Context
	tags []string
}

// An iteration accepts any cancelable context: one whose dynamic type is not comparable waits for each event like any other.
func TestIterationOnAnUncomparableContextWaitsForEachEvent(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		base, cancel := context.WithCancel(context.Background())
		defer cancel()
		ctx := uncomparableContext{Context: base, tags: []string{"request"}}
		stream := NewAssistantMessageEventStream()
		partial := testAssistant(StopReasonPending)
		delivered := 0
		var failure any
		finished := make(chan struct{})
		go func() {
			defer close(finished)
			defer func() { failure = recover() }()
			for range stream.Events(ctx) {
				delivered++
			}
		}()
		waitForStreamWaiters(t, stream, 1)
		if err := stream.Push(StartEvent{Partial: partial}); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if failure == nil {
			waitForStreamWaiters(t, stream, 1)
			pushTestDone(t, stream)
		}
		<-finished
		if failure != nil {
			t.Fatalf("the second wait panicked: %v", failure)
		}
		if delivered != 2 {
			t.Fatalf("delivered %d events, want 2", delivered)
		}
	})
}
