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

// Run waits for the reserved continuation, runs its synchronous callback with an observation scope, and releases ownership on return. A canceled reservation returns context.Canceled without invoking the callback. The callback runs as a coroutine turn, so a long-lived callback that awaits many times hands execution to other turns without goroutine wakes; it identifies its goroutine once.
func (continuation *StreamContinuation) Run(body func(*StreamObservation) error) error {
	if !continuation.started.CompareAndSwap(false, true) {
		return errors.New("stream continuation already consumed")
	}
	result := error(context.Canceled)
	continuation.turn.run(func(turn *continuationTurn) {
		result = continuation.observe(turn, currentGoroutineID(), body)
	})
	return result
}

// StreamOwner identifies the goroutine that runs stream callbacks, so a goroutine that runs one callback after another identifies itself once instead of at each callback.
type StreamOwner struct{ id uint64 }

// CurrentStreamOwner is the owner of the calling goroutine.
func CurrentStreamOwner() StreamOwner { return StreamOwner{currentGoroutineID()} }

// RunOwned is Run for a caller that already holds its own StreamOwner: owner must be CurrentStreamOwner() of the goroutine that calls RunOwned, which is the goroutine the callback runs on. The callback runs directly on that goroutine, for a caller that runs one short callback after another.
func (continuation *StreamContinuation) RunOwned(owner StreamOwner, body func(*StreamObservation) error) error {
	if !continuation.started.CompareAndSwap(false, true) {
		return errors.New("stream continuation already consumed")
	}
	result := error(context.Canceled)
	continuation.turn.runDirect(func(turn *continuationTurn) {
		result = continuation.observe(turn, owner.id, body)
	})
	return result
}

// observe runs the callback under a new observation of turn that owner runs.
func (continuation *StreamContinuation) observe(turn *continuationTurn, owner uint64, body func(*StreamObservation) error) error {
	observation := &StreamObservation{turn: turn, owner: owner}
	turn.executor.mu.Lock()
	turn.executor.observation = observation
	turn.executor.mu.Unlock()
	if continuation.onReturn != nil {
		defer continuation.onReturn()
	}
	defer observation.end()
	return body(observation)
}

// Cancel releases an unclaimed reservation without waiting for its queue position. It returns false once Run owns the callback; that running callback must be canceled and joined by its caller.
func (continuation *StreamContinuation) Cancel() bool {
	return continuation.turn.abandon()
}
