package ai

import (
	"context"
	"errors"
	"sync/atomic"
)

// StreamContinuation reserves a FIFO continuation before its owner transfers work to another goroutine. Its owner must Run it once or Cancel it before abandoning queued work.
type StreamContinuation struct {
	turn    *continuationTurn
	started atomic.Bool
	// onReturn queues the owner's resume reaction when the callback returns, before the turn releases execution. AwaitContinuation sets it before the callback can run.
	onReturn func()
}

// WithStreamContinuations attaches a shared reaction queue without acquiring execution. Attach it before publishing a stable signal context that producers and subscribers must share by identity.
func WithStreamContinuations(ctx context.Context) context.Context {
	ctx, _ = withContinuationExecutor(ctx)
	return ctx
}

// RunStreamContinuation owns a caller's synchronous stream-creation prefix through iteration. The context must carry the shared reaction queue from WithStreamContinuations. The callback uses observation.Context(ctx) for its iterator and Yield when awaiting an already-returned stream.
func RunStreamContinuation(ctx context.Context, body func(*StreamObservation) error) error {
	_, executor := withContinuationExecutor(ctx)
	continuation := &StreamContinuation{turn: executor.newTurn()}
	return continuation.Run(body)
}

// PrepareContinuation reserves the next callback's place while the current observation still owns its synchronous prefix.
func (observation *StreamObservation) PrepareContinuation() *StreamContinuation {
	return &StreamContinuation{turn: observation.turn.executor.newTurn()}
}

// Run waits for the reserved continuation, runs its synchronous callback with an observation scope, and releases ownership on return. A canceled reservation returns context.Canceled without invoking the callback.
func (continuation *StreamContinuation) Run(body func(*StreamObservation) error) error {
	if !continuation.started.CompareAndSwap(false, true) {
		return errors.New("stream continuation already consumed")
	}
	result := error(context.Canceled)
	continuation.turn.run(func(turn *continuationTurn) {
		observation := &StreamObservation{turn: turn, owner: goroutineID()}
		turn.executor.mu.Lock()
		turn.executor.observation = observation
		turn.executor.mu.Unlock()
		if continuation.onReturn != nil {
			defer continuation.onReturn()
		}
		defer observation.end()
		result = body(observation)
	})
	return result
}

// Cancel releases an unclaimed reservation without waiting for its queue position. It returns false once Run owns the callback; that running callback must be canceled and joined by its caller.
func (continuation *StreamContinuation) Cancel() bool {
	return continuation.turn.abandon()
}
