package ai

import (
	"context"
	"slices"
)

// continuationEventIterator models EventStream's async generator for one consumer loop. A buffered event awaits its yield value; a waiting iterator first awaits delivery and then awaits the yield value. The iterator owns the promises and the waiter of its current next: the consumer awaits next at once, and when it resumes every reaction of those promises has run, so the following next reuses them.
type continuationEventIterator struct {
	stream   *AssistantMessageEventStream
	executor *continuationExecutor
	ctx      context.Context
	// stopCancel stops the cancellation hook that the first wait registers on ctx.
	stopCancel func() bool
	result     continuationPromise[eventDelivery]
	waiting    continuationPromise[eventDelivery]
	value      continuationPromise[eventDelivery]
	waiter     eventWaiter
	// The promise reactions, bound once.
	resolveResult func(eventDelivery)
	delivered     func(eventDelivery)
}

// newContinuationEventIterator starts a consumer loop over stream. Canceling ctx ends a pending wait without an event; close ends the loop.
func newContinuationEventIterator(ctx context.Context, stream *AssistantMessageEventStream, executor *continuationExecutor) *continuationEventIterator {
	iterator := &continuationEventIterator{stream: stream, executor: executor, ctx: ctx}
	iterator.resolveResult = iterator.result.resolve
	iterator.delivered = iterator.deliver
	iterator.waiter.onReady = iterator.ready
	return iterator
}

func (iterator *continuationEventIterator) close() {
	if iterator.stopCancel != nil {
		iterator.stopCancel()
	}
}

// next is one iterator.next() call: its promise settles with the next event, or with done once the stream ends or ctx is canceled during a wait.
func (iterator *continuationEventIterator) next() *continuationPromise[eventDelivery] {
	iterator.result = continuationPromise[eventDelivery]{executor: iterator.executor}
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
		iterator.yield(eventDelivery{event: event})
		return &iterator.result
	}
	if stream.terminal {
		stream.mu.Unlock()
		iterator.result.resolve(eventDelivery{done: true})
		return &iterator.result
	}
	iterator.waiting = continuationPromise[eventDelivery]{executor: iterator.executor}
	if iterator.ctx.Err() != nil {
		// The cancellation hook already ran; a wait it would end at once ends now.
		stream.mu.Unlock()
		iterator.waiting.resolve(eventDelivery{done: true})
	} else {
		if iterator.stopCancel == nil {
			iterator.stopCancel = context.AfterFunc(iterator.ctx, iterator.cancel)
		}
		stream.waiters = append(stream.waiters, &iterator.waiter)
		stream.mu.Unlock()
	}
	iterator.waiting.onResolved(iterator.delivered)
	return &iterator.result
}

// ready settles the pending wait with the event a push assigned to it under the stream lock.
func (iterator *continuationEventIterator) ready(delivery eventDelivery) func() {
	return iterator.waiting.resolveDeferred(delivery)
}

// cancel ends a pending wait when ctx is canceled. A wait that a push already assigned an event to keeps it: the assignment is its place in the shared FIFO.
func (iterator *continuationEventIterator) cancel() {
	stream := iterator.stream
	stream.mu.Lock()
	index := slices.Index(stream.waiters, &iterator.waiter)
	if index < 0 {
		stream.mu.Unlock()
		return
	}
	stream.waiters = slices.Delete(stream.waiters, index, index+1)
	stream.mu.Unlock()
	iterator.waiting.resolve(eventDelivery{done: true})
}

func (iterator *continuationEventIterator) deliver(delivery eventDelivery) {
	if delivery.done {
		iterator.result.resolve(delivery)
		return
	}
	iterator.yield(delivery)
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

// yield settles next's promise through the generator's yield value, one reaction later.
func (iterator *continuationEventIterator) yield(delivery eventDelivery) {
	iterator.value = continuationPromise[eventDelivery]{executor: iterator.executor, resolved: true, value: delivery}
	iterator.value.onResolved(iterator.resolveResult)
}
