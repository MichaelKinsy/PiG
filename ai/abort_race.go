package ai

import "context"

// OperationSignal normalizes an optional public signal without imposing a deadline: a nil signal becomes a context that is never cancelled.
//
// upstream: packages/ai/src/utils/abort.ts:operationSignal
func OperationSignal(signal context.Context) context.Context {
	if signal == nil {
		return context.Background()
	}
	return signal
}

// RaceWithAbortSignal runs operation and stops waiting for it when signal is cancelled. The operation is never interrupted: it runs to completion on its own goroutine and its result is dropped, so an abandoned operation is observed through settlement as in the upstream promise race. The cancellation cause is returned when the signal wins, including a signal that is already cancelled when the race starts; otherwise the operation's value and error are returned. A nil signal waits for the operation alone.
//
// upstream: packages/ai/src/utils/abort.ts:raceWithAbortSignal
func RaceWithAbortSignal[T any](signal context.Context, operation func() (T, error)) (T, error) {
	var zero T
	if signal == nil {
		return operation()
	}
	type outcome struct {
		value T
		err   error
	}
	settled := make(chan outcome, 1)
	go func() {
		value, err := operation()
		settled <- outcome{value, err}
	}()
	if signal.Err() != nil {
		return zero, context.Cause(signal)
	}
	select {
	case result := <-settled:
		return result.value, result.err
	case <-signal.Done():
		return zero, context.Cause(signal)
	}
}
