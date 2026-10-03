// Package chordctx ports packages/chord/src/context/index.ts onto context.Context. A chord Context is a Go context: Background and the context package's to-do root are the standard roots, and an abort signal is cancellation, with the abort reason carried as the cancellation cause.
package chordctx

import (
	"context"
)

// Key is the typed identity of one value carried by a context. Two keys never collide, even with equal descriptions or types.
type Key[T any] struct{ token *keyToken }

type keyToken struct{ description string }

// NewKey creates a key. The description names it in diagnostics only.
func NewKey[T any](description string) Key[T] {
	return Key[T]{token: &keyToken{description: description}}
}

// Description is the name given to NewKey.
func (key Key[T]) Description() string { return key.token.description }

// WithValue derives a context containing one additional or replaced value. The parent is unchanged.
func WithValue[T any](parent context.Context, key Key[T], value T) context.Context {
	return context.WithValue(parent, key.token, value)
}

// Value returns the value stored under key and whether one exists.
func Value[T any](ctx context.Context, key Key[T]) (T, bool) {
	value, ok := ctx.Value(key.token).(T)
	return value, ok
}

// WithAbortSignal derives a context cancelled by either the parent or signal, whichever is first, with that one's cause. The parent is unchanged. A signal that is already cancelled cancels the result before it is returned, as AbortSignal.any does with an aborted input.
func WithAbortSignal(parent, signal context.Context) context.Context {
	derived, cancel := context.WithCancelCause(parent)
	if signal.Err() != nil {
		cancel(context.Cause(signal))
		return derived
	}
	stop := context.AfterFunc(signal, func() { cancel(context.Cause(signal)) })
	context.AfterFunc(derived, func() { stop() })
	return derived
}

// WithoutAbortSignal derives a context that keeps every value but ignores caller cancellation. It is for mandatory cleanup only.
func WithoutAbortSignal(parent context.Context) context.Context { return context.WithoutCancel(parent) }

// WithCancel derives an independently cancellable child. cancel records cause, and a nil cause records context.Canceled.
func WithCancel(parent context.Context) (ctx context.Context, cancel func(cause error)) {
	return context.WithCancelCause(parent)
}

// Settled is the outcome of a unit of work: the Go form of a settled promise.
type Settled[T any] struct {
	Value T
	Err   error
}

// Await observes work until it settles or the invocation is cancelled. Cancellation returns the cancellation cause from this waiter only; it does not cancel the work, which may still settle later. A nil ctx waits for the work alone.
func Await[T any](ctx context.Context, work <-chan Settled[T]) (T, error) {
	var zero T
	if ctx == nil {
		outcome := <-work
		return outcome.Value, outcome.Err
	}
	if err := ctx.Err(); err != nil {
		return zero, context.Cause(ctx)
	}
	select {
	case outcome := <-work:
		return outcome.Value, outcome.Err
	case <-ctx.Done():
		return zero, context.Cause(ctx)
	}
}
