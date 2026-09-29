package ai

import "context"

// continuationEventIterator models EventStream's async generator. A buffered event awaits its yield value; a waiting iterator first awaits delivery and then awaits the yield value.
type continuationEventIterator struct {
	stream   *AssistantMessageEventStream
	executor *continuationExecutor
}

func (iterator continuationEventIterator) next(ctx context.Context) *continuationPromise[eventDelivery] {
	next := newContinuationPromise[eventDelivery](iterator.executor)
	stream := iterator.stream
	stream.mu.Lock()
	if len(stream.queue) > 0 {
		event := stream.queue[0]
		stream.queue[0] = nil
		stream.queue = stream.queue[1:]
		if len(stream.queue) == 0 {
			stream.queue = nil
		}
		stream.mu.Unlock()
		iterator.yield(next, eventDelivery{event: event})
		return next
	}
	if stream.terminal {
		stream.mu.Unlock()
		next.resolve(eventDelivery{done: true})
		return next
	}
	waiting := newContinuationPromise[eventDelivery](iterator.executor)
	waiter := &eventWaiter{ready: make(chan eventDelivery, 1)}
	stopCancel := context.AfterFunc(ctx, func() {
		delivery, assigned := stream.cancelWaiter(waiter)
		if !assigned {
			delivery = eventDelivery{done: true}
		}
		waiting.resolve(delivery)
	})
	waiter.onReady = func(delivery eventDelivery) func() {
		stopCancel()
		return waiting.resolveDeferred(delivery)
	}
	stream.waiters = append(stream.waiters, waiter)
	stream.mu.Unlock()
	waiting.onResolved(func(delivery eventDelivery) {
		if delivery.done {
			next.resolve(delivery)
		} else {
			iterator.yield(next, delivery)
		}
	})
	return next
}

func (stream *AssistantMessageEventStream) resolveResultLocked() []func() {
	stream.resolved = true
	stream.clearPublicationsLocked()
	close(stream.done)
	var continuations []func()
	for _, waiter := range stream.resultWaiters {
		if continuation := waiter.resolveDeferred(stream.result); continuation != nil {
			continuations = append(continuations, continuation)
		}
	}
	stream.resultWaiters = nil
	return continuations
}

func (stream *AssistantMessageEventStream) resultContinuation(executor *continuationExecutor) *continuationPromise[*AssistantMessage] {
	result := newContinuationPromise[*AssistantMessage](executor)
	stream.mu.Lock()
	if !stream.resolved {
		stream.resultWaiters = append(stream.resultWaiters, result)
		stream.mu.Unlock()
		return result
	}
	continuation := result.resolveDeferred(stream.result)
	stream.mu.Unlock()
	if continuation != nil {
		continuation()
	}
	return result
}

func (iterator continuationEventIterator) yield(next *continuationPromise[eventDelivery], delivery eventDelivery) {
	value := newContinuationPromise[eventDelivery](iterator.executor)
	value.resolve(delivery)
	value.onResolved(next.resolve)
}
