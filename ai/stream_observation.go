package ai

import (
	"context"
	"iter"
	"sync"
)

// StreamObservation owns the synchronous part of a stream-consumer continuation. Await releases execution while an upstream asynchronous operation waits, then resumes through the same reaction queue.
type StreamObservation struct {
	turn *continuationTurn
	// owner is the goroutine that runs the callback this observation scopes.
	owner uint64

	mu          sync.Mutex
	suspensions int
	released    chan struct{}
	acquired    chan struct{}
	acquiring   bool
	ended       bool
	completion  completionClass
}

// completionClass is the callback queue that settles the promise an asynchronous wait resolves.
type completionClass int

const (
	completionReady completionClass = iota
	completionTick
	completionExternal
)

type streamObservationContextKey struct{}

// Context carries the active observation through host callbacks without adding a wire field. A nil observation preserves the caller's context identity.
func (observation *StreamObservation) Context(ctx context.Context) context.Context {
	if observation == nil {
		return ctx
	}
	ctx = context.WithValue(ctx, continuationExecutorKey{}, observation.turn.executor)
	ctx = context.WithValue(ctx, continuationTurnKey{}, observation.turn)
	return context.WithValue(ctx, streamObservationContextKey{}, observation)
}

// StreamObservationFromContext returns the host observation, if this callback belongs to a scoped stream continuation. A callback that runs while a provider response turn holds execution belongs to that turn, even when its context was derived from a consumer that is parked in an await: an awaiting host callback must release the turn that is running.
func StreamObservationFromContext(ctx context.Context) *StreamObservation {
	if ctx == nil {
		return nil
	}
	observation, _ := ctx.Value(streamObservationContextKey{}).(*StreamObservation)
	if observation != nil {
		return observation.callbackObservation()
	}
	if executor, ok := ctx.Value(continuationExecutorKey{}).(*continuationExecutor); ok {
		executor.mu.Lock()
		observation = executor.observation
		executor.mu.Unlock()
		if observation == nil {
			return executor.activeProducer()
		}
		return observation.callbackObservation()
	}
	return observation
}

// callbackObservation resolves the observation a host callback derived from this one awaits through.
func (observation *StreamObservation) callbackObservation() *StreamObservation {
	executor := observation.turn.executor
	executor.mu.Lock()
	active := executor.active
	executor.mu.Unlock()
	if active == observation.turn {
		return observation
	}
	if producer := executor.activeProducer(); producer != nil {
		return producer
	}
	if observation.suspendedOrResuming() {
		// No other turn runs, so a nested await is a no-op.
		return observation
	}
	return nil
}

// suspendedOrResuming reports whether the observation released execution, is reacquiring it, or has ended.
func (observation *StreamObservation) suspendedOrResuming() bool {
	observation.mu.Lock()
	defer observation.mu.Unlock()
	return observation.suspensions > 0 || observation.acquiring || observation.ended
}

// activeProducer returns the observation of the provider response turn that holds execution, or nil.
func (executor *continuationExecutor) activeProducer() *StreamObservation {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	if executor.active == nil {
		return nil
	}
	return executor.active.producer
}

// ownedByCaller reports whether the calling goroutine runs the callback this observation scopes.
func (observation *StreamObservation) ownedByCaller() bool {
	return observation.owner != 0 && observation.owner == goroutineID()
}

// Yield awaits an already resolved operation. It reserves the resume reaction before releasing execution, so synchronous completion never depends on goroutine admission.
func (observation *StreamObservation) Yield() {
	if observation == nil {
		return
	}
	executor := observation.turn.executor
	executor.mu.Lock()
	if executor.observation == observation {
		executor.observation = nil
	}
	executor.mu.Unlock()
	suspendContinuation(observation.turn)
	executor.mu.Lock()
	executor.observation = observation
	executor.mu.Unlock()
}

// Await runs a blocking wait outside the synchronous observation and resumes in FIFO continuation order. Prepare any values that upstream reads before its await before calling Await.
func (observation *StreamObservation) Await(wait func() error) error {
	resume := observation.suspend()
	err := wait()
	resume(true)
	return err
}

// AwaitContinuation is Await for a wait that ends when continuation's callback returns. The resume reaction is queued when the callback returns, before its turn releases execution, so the awaiting goroutine keeps its place ahead of any I/O completion that became ready while the callback ran, however late that goroutine is scheduled. An unclaimed or canceled continuation resumes when wait returns, as in Await.
func (observation *StreamObservation) AwaitContinuation(continuation *StreamContinuation, wait func() error) error {
	resume := observation.suspendWith(func(resume func(bool)) {
		if continuation != nil {
			continuation.onReturn = func() { resume(false) }
		}
	})
	err := wait()
	resume(true)
	return err
}

// AwaitExternal is Await for a wait that Pi settles from an I/O completion or timer callback instead of a promise reaction. The continuation runs after every ready reaction, and every reaction those queue, has run, so a wait that ends while the provider's reactions are still running resumes deterministically at their end. An extension handler that awaits its process is such a wait. wait runs outside the observation, as in Await.
func (observation *StreamObservation) AwaitExternal(wait func() error) error {
	resume := observation.suspend()
	err := wait()
	observation.mu.Lock()
	observation.completion = completionExternal
	observation.mu.Unlock()
	resume(true)
	return err
}

// AwaitTick is Await for a wait that Pi settles from a process.nextTick callback: the continuation runs after every ready reaction and before any I/O completion, including completions queued earlier. A synchronous pipe or file write's callback is such a wait (output-guard.ts:writeRawStdoutChunk). Node queues the callback while the caller still runs, so wait runs first, on the caller's goroutine and still holding the observation; it must not depend on the executor.
func (observation *StreamObservation) AwaitTick(wait func() error) error {
	err := wait()
	for {
		observation.mu.Lock()
		if observation.ended || observation.suspensions > 0 {
			observation.mu.Unlock()
			return err
		}
		if observation.acquiring {
			acquired := observation.acquired
			observation.mu.Unlock()
			<-acquired
			continue
		}
		observation.released = make(chan struct{})
		observation.acquired = make(chan struct{})
		released, acquired := observation.released, observation.acquired
		observation.completion = completionTick
		drain := observation.acquireLocked()
		observation.mu.Unlock()
		if drain != nil {
			drain()
		}
		observation.turn.release()
		close(released)
		<-acquired
		return err
	}
}

func (observation *StreamObservation) suspend() func(bool) {
	return observation.suspendWith(nil)
}

// suspendWith is suspend that hands the resume function to before ahead of releasing execution, so work the release admits can call it.
func (observation *StreamObservation) suspendWith(before func(resume func(bool))) func(bool) {
	observation.mu.Lock()
	if observation.ended || observation.suspensions > 0 {
		observation.mu.Unlock()
		noop := func(bool) {}
		if before != nil {
			before(noop)
		}
		return noop
	}
	if observation.acquiring {
		acquired := observation.acquired
		observation.mu.Unlock()
		<-acquired
		return observation.suspendWith(before)
	}
	first := observation.suspensions == 0
	if first {
		observation.released = make(chan struct{})
		observation.acquired = make(chan struct{})
	}
	observation.suspensions++
	released := observation.released
	observation.mu.Unlock()
	var once sync.Once
	var acquired chan struct{}
	resume := func(wait bool) {
		once.Do(func() {
			observation.mu.Lock()
			observation.suspensions--
			suspending := observation.suspensions
			var drain func()
			if suspending == 0 && !observation.acquiring && !observation.ended {
				drain = observation.acquireLocked()
			}
			acquired = observation.acquired
			observation.mu.Unlock()
			if drain != nil {
				drain()
			}
		})
		if wait {
			<-acquired
		}
	}
	if before != nil {
		before(resume)
	}
	if first {
		observation.turn.release()
		close(released)
	} else {
		<-released
	}
	return resume
}

func (observation *StreamObservation) acquireLocked() func() {
	observation.acquiring = true
	turn := observation.turn
	executor := turn.executor
	class := observation.completion
	observation.completion = completionReady
	post := executor.postDeferred
	acquire := func() {
		turn.executor.mu.Lock()
		if turn.executor.active != nil {
			turn.executor.mu.Unlock()
			panic("stream observation resumed during another turn")
		}
		turn.executor.active = turn
		turn.executor.observation = observation
		turn.executor.mu.Unlock()
		observation.mu.Lock()
		observation.acquiring = false
		close(observation.acquired)
		observation.released = nil
		observation.mu.Unlock()
	}
	switch class {
	case completionTick:
		return executor.postTickDeferred(acquire)
	case completionExternal:
		return executor.postExternalDeferred(acquire)
	}
	return post(acquire)
}

func (observation *StreamObservation) end() {
	observation.mu.Lock()
	observation.ended = true
	if observation.released != nil {
		released := observation.released
		observation.mu.Unlock()
		<-released
		observation.mu.Lock()
		var drain func()
		if !observation.acquiring && observation.released != nil {
			drain = observation.acquireLocked()
		}
		acquired := observation.acquired
		observation.mu.Unlock()
		if drain != nil {
			drain()
		}
		<-acquired
	} else {
		observation.mu.Unlock()
	}
	observation.turn.executor.mu.Lock()
	if observation.turn.executor.observation == observation {
		observation.turn.executor.observation = nil
	}
	observation.turn.executor.mu.Unlock()
}

// ObservationContext attaches this stream's continuation executor to owned setup and forwarding work.
func (stream *AssistantMessageEventStream) ObservationContext(ctx context.Context) context.Context {
	stream.mu.Lock()
	if stream.executor == nil {
		_, stream.executor = withContinuationExecutor(ctx)
	}
	executor := stream.executor
	stream.mu.Unlock()
	return context.WithValue(ctx, continuationExecutorKey{}, executor)
}

// ForwardStream awaits and forwards one source iterator without observing or cloning its partial references. Its caller owns setup, cancellation, errors and draining. A caller that owns the running turn forwards in it, so the source it just created cannot advance before the first await; any other caller acquires the queue, and a source it created earlier may already have run ahead.
func (stream *AssistantMessageEventStream) ForwardStream(ctx context.Context, source *AssistantMessageEventStream) error {
	ctx = stream.ObservationContext(ctx)
	executor := stream.executor
	var failure error
	forward := func(turn *continuationTurn) {
		observation := context.WithValue(context.WithoutCancel(ctx), continuationTurnKey{}, turn)
		for event := range source.events(observation, false) {
			if err := stream.Push(event); err != nil {
				failure = err
				return
			}
		}
		stream.End(awaitContinuation(turn, source.resultContinuation(executor)))
	}
	if owned := ownedContinuationTurn(ctx, executor); owned != nil {
		forward(owned)
	} else {
		executor.run(forward)
	}
	return failure
}

// ObserveEvents yields the same FIFO as Events and exposes the continuation scope for callers that await asynchronous event handling.
func (stream *AssistantMessageEventStream) ObserveEvents(ctx context.Context) iter.Seq2[*StreamObservation, AssistantMessageEvent] {
	return stream.observeEvents(ctx, true)
}

func (stream *AssistantMessageEventStream) observeEvents(ctx context.Context, materialize bool) iter.Seq2[*StreamObservation, AssistantMessageEvent] {
	if ctx == nil {
		panic("assistant message event stream: nil iterator context")
	}
	return func(yield func(*StreamObservation, AssistantMessageEvent) bool) {
		stream.mu.Lock()
		executor := stream.executor
		if executor == nil {
			_, executor = withContinuationExecutor(ctx)
			stream.executor = executor
		}
		stream.mu.Unlock()
		consume := func(turn *continuationTurn) {
			executor := turn.executor
			iterator := continuationEventIterator{stream: stream, executor: executor}
			owner := goroutineID()
			for {
				delivery := awaitContinuation(turn, iterator.next(ctx))
				if delivery.done {
					return
				}
				observation := &StreamObservation{turn: turn, owner: owner}
				executor.mu.Lock()
				executor.observation = observation
				executor.mu.Unlock()
				more := func() bool {
					defer observation.end()
					return yield(observation, stream.deliver(delivery.event, materialize))
				}()
				if !more {
					return
				}
			}
		}
		if turn, ok := ctx.Value(continuationTurnKey{}).(*continuationTurn); ok && turn != nil {
			consume(turn)
		} else {
			executor.run(consume)
		}
	}
}
