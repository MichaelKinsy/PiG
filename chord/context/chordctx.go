// Package chordctx ports packages/chord/src/context/index.ts (`@earendil-works/chord/context`) onto context.Context. A chord Context is a Go context: an abort signal is cancellation, with the abort reason carried as the cancellation cause, and `toString` is the context's String method, which prints Pi's text.
// Background and the placeholder root are Pi's BACKGROUND_CONTEXT and TODO_CONTEXT roots; contexts derived from them by this package print as Pi's contexts do. A context derived from another root, such as context.Background, prints that root's own text first.
package chordctx

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/MichaelKinsy/PiG/chord/internal/parking"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// emptyContext is Pi's EmptyContext: a root with no values and no cancellation, printed as its name.
type emptyContext struct{ name string }

func (*emptyContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (*emptyContext) Done() <-chan struct{}       { return nil }
func (*emptyContext) Err() error                  { return nil }
func (*emptyContext) Value(any) any               { return nil }
func (c *emptyContext) String() string            { return c.name }

var (
	backgroundContext = &emptyContext{name: "[Context BACKGROUND_CONTEXT]"}
	todoContext       = &emptyContext{name: "[Context TODO_CONTEXT]"}
)

// Background is Pi's BACKGROUND_CONTEXT: the root context of work that has no caller.
func Background() context.Context { return backgroundContext }

// This function is Pi's TODO_CONTEXT: a root context for code that has no context to receive yet. It is distinct from Background.
func TODO() context.Context { return todoContext }

// valueContext is Pi's ContextValue: one value layered over a parent, printed as `<parent>.WithValue(<key description>)`.
type valueContext struct {
	context.Context
	key   Token
	value any
}

func (c *valueContext) Value(key any) any {
	if key == c.key {
		return c.value
	}
	return c.Context.Value(key)
}

func (c *valueContext) String() string {
	return fmt.Sprint(c.Context) + ".WithValue(" + c.key.Description() + ")"
}

// abortContext is the context withAbortSignal, withoutAbortSignal and withCancel derive in Pi: a ContextValue of the abort signal key, printed as `<parent>.WithValue(chord.abortSignal)`. Cancellation, deadline and values are the embedded derived context's.
type abortContext struct {
	context.Context
	parent context.Context
	// signal is the other source of a WithAbortSignal context; nil otherwise.
	signal context.Context
	// order is the place of this context's own cancel in the order of aborts (see AbortOrder); 0 until it was cancelled.
	order atomic.Uint64
}

// aborts numbers the aborts of the contexts WithCancel makes, in the order their cancel functions ran.
var aborts atomic.Uint64

// AbortOrder is when ctx was aborted relative to other contexts of this package: the number of the earliest abort that
// ended it (its own cancel, or the one of a parent or signal it derives from), 0 for a context not aborted by this
// package's cancel functions. AbortSignal.any takes the reason of the source that aborted first; a Go context derived from
// two sources is cancelled by listeners on goroutines of their own, which may run in any order, so the one that decides the
// reason compares AbortOrder of the sources instead.
func AbortOrder(ctx context.Context) uint64 {
	var earliest uint64
	for ctx != nil {
		c, ok := ctx.(*abortContext)
		if !ok {
			return earliest
		}
		if order := c.order.Load(); order != 0 && (earliest == 0 || order < earliest) {
			earliest = order
		}
		if c.signal != nil {
			if order := AbortOrder(c.signal); order != 0 && (earliest == 0 || order < earliest) {
				earliest = order
			}
		}
		ctx = c.parent
	}
	return earliest
}

func (c *abortContext) String() string { return fmt.Sprint(c.parent) + ".WithValue(chord.abortSignal)" }

// Key is the typed identity of one value carried by a context. Two keys never collide, even with equal descriptions or types.
type Key[T any] struct{ token *keyToken }

type keyToken struct{ description string }

// Token is ContextKey.token in Pi: the identity of a key. Two tokens are equal exactly when they are the token of the same key, and the description is the one the key was created with, as `token.description` is.
type Token struct{ id *keyToken }

// Description is the name given to the key the token belongs to.
func (token Token) Description() string { return token.id.description }

// Token is the key's identity: the value a context stores a key's value under.
func (key Key[T]) Token() Token { return Token{key.token} }

// NewKey creates a key. The description names it in diagnostics only.
func NewKey[T any](description string) Key[T] {
	return Key[T]{token: &keyToken{description: description}}
}

// Description is the name given to NewKey.
func (key Key[T]) Description() string { return key.token.description }

// WithValue derives a context containing one additional or replaced value. The parent is unchanged.
func WithValue[T any](parent context.Context, key Key[T], value T) context.Context {
	return &valueContext{Context: parent, key: key.Token(), value: value}
}

// Value returns the value stored under key and whether one exists.
func Value[T any](ctx context.Context, key Key[T]) (T, bool) {
	value, ok := ctx.Value(key.Token()).(T)
	return value, ok
}

// WithAbortSignal derives a context cancelled by either the parent or signal, whichever is first, with that one's cause. The parent is unchanged. A signal that is already cancelled cancels the result before it is returned, as AbortSignal.any does with an aborted input.
func WithAbortSignal(parent, signal context.Context) context.Context {
	derived, cancel := context.WithCancelCause(parent)
	if signal.Err() != nil {
		cancel(context.Cause(signal))
		return &abortContext{Context: derived, parent: parent, signal: signal}
	}
	stop := context.AfterFunc(signal, func() { cancel(context.Cause(signal)) })
	context.AfterFunc(derived, func() { stop() })
	return &abortContext{Context: derived, parent: parent, signal: signal}
}

// WithoutAbortSignal derives a context that keeps every value but ignores caller cancellation. It is for mandatory cleanup only.
func WithoutAbortSignal(parent context.Context) context.Context {
	return &abortContext{Context: context.WithoutCancel(parent), parent: parent}
}

// WithCancel derives an independently cancellable child. cancel records cause, and a nil cause records context.Canceled.
func WithCancel(parent context.Context) (ctx context.Context, cancel func(cause error)) {
	derived, cancelCause := context.WithCancelCause(parent)
	child := &abortContext{Context: derived, parent: parent}
	return child, func(cause error) {
		child.order.CompareAndSwap(0, aborts.Add(1))
		cancelCause(cause)
	}
}

// telemetryKey is Pi's TELEMETRY_CONTEXT_KEY: the key under which a context carries its telemetry parent. It prints as `.WithValue(chord.telemetry)`.
var telemetryKey = NewKey[telemetry.TelemetrySpan]("chord.telemetry")

// Telemetry is Pi's `Context.telemetry`: the explicit telemetry parent of ctx, always a span (the no-op span is one), so a caller never narrows it. It is never nil: a context that carries none, context.Background() and the to-do context included, returns telemetry.NoopTelemetryContext, as Pi's BaseContext defaults to NOOP_TELEMETRY_CONTEXT.
// Pi's Context is a closed interface that custom implementations must extend with a `telemetry: TelemetrySpan` member (a Breaking Change upstream, whose type narrowed from TelemetryContext when Context.telemetry became always a span, so withTelemetry accepts spans and not adapters). A Go context is stdlib's open interface, so a context from outside this package carries no telemetry and reads as the no-op parent; no Go caller breaks.
func Telemetry(ctx context.Context) telemetry.TelemetrySpan {
	if value, ok := Value(ctx, telemetryKey); ok {
		return value
	}
	return telemetry.NoopTelemetryContext
}

// WithTelemetry derives an explicit telemetry parent without changing cancellation or application values: it is Pi's withTelemetry. The parent is unchanged, and a context derived from the result by this package keeps the telemetry until another WithTelemetry replaces it.
// Pi types the result as `Context & { telemetry: T }` for the concrete T it was given; Go has no intersection type, so a caller that needs the concrete T keeps its own reference, and Telemetry(result) is the TelemetryContext interface. The argument order is the parent first, as WithValue and WithAbortSignal take it; Pi takes the telemetry first.
func WithTelemetry(parent context.Context, t telemetry.TelemetrySpan) context.Context {
	return WithValue(parent, telemetryKey, t)
}

// TraceIn runs a child span of Telemetry(ctx) and passes fn a derived context whose telemetry is that span: Pi's traceIn. It returns fn's result, and fn's error as the identical value, after the span ends, as telemetry.StartSpan does.
// Pi's callback receives one `Context & { telemetry: TelemetrySpan }`, narrowed by the type system. Go cannot narrow a context's type, so the callback receives the context and reads the span as Telemetry(child), which is always a span.
func TraceIn[T any](ctx context.Context, options telemetry.SpanOptions, fn func(child context.Context) (T, error)) (T, error) {
	return telemetry.StartSpan(Telemetry(ctx), options, func(span telemetry.TelemetrySpan) (T, error) {
		return fn(WithTelemetry(ctx, span))
	})
}

// Settled is the outcome of a unit of work: the Go form of a settled promise.
type Settled[T any] struct {
	Value T
	Err   error
}

// Suspend is the first suspension of a method handler: Pi runs an async handler synchronously, inside the section that admits its call, up to its first `await`, and the rest of that section waits for it. A Go handler is blocking code that runs as a coroutine of the section's goroutine, and the section waits until it returns or suspends. The host primitives that wait (Get, Call, Sleep, UntilAborted, transactions, Await, Receive) suspend the handler themselves; call Suspend with the handler's ctx, or a context derived from it, right before waiting for anything else (a channel, a condition, I/O that another goroutine of the host's section must not outwait). Code that waits without suspending is a synchronous block of the section, as a synchronous loop is in Pi: the host's other work does not run until it ends. Suspend of a context that carries no handler does nothing, and a second call does nothing. Only the handler's own goroutine suspends it: a goroutine the handler started leaves the handler's synchronous part to the handler's own next wait.
func Suspend(ctx context.Context) { parking.Park(ctx) }

// Await observes work until it settles or the invocation is cancelled. Cancellation returns the cancellation cause from this waiter only; it does not cancel the work, which may still settle later. A nil ctx waits for the work alone.
// A method handler's ctx parks the handler where it starts to wait: Pi runs an async handler synchronously up to its first `await` inside the section that admits its call, and the Go handler is blocking code that parks at the first wait of a host primitive or of Await or Receive. Code that waits for something else (a raw channel receive, a mutex) is a synchronous block of the section, as a synchronous loop is in Pi.
func Await[T any](ctx context.Context, work <-chan Settled[T]) (T, error) {
	var zero T
	if ctx == nil {
		outcome := <-work
		return outcome.Value, outcome.Err
	}
	if err := ctx.Err(); err != nil {
		return zero, context.Cause(ctx)
	}
	parking.Park(ctx)
	select {
	case outcome := <-work:
		return outcome.Value, outcome.Err
	case <-ctx.Done():
		return zero, context.Cause(ctx)
	}
}

// Receive is Await for a plain channel: `await promise` of work that cannot reject, as a gate a test resolves. It returns the received value, or the cancellation cause of ctx from this waiter only. A closed channel yields the zero value, as a resolved promise of nothing. A nil ctx waits for the channel alone.
func Receive[T any](ctx context.Context, work <-chan T) (T, error) {
	var zero T
	if ctx == nil {
		return <-work, nil
	}
	if err := ctx.Err(); err != nil {
		return zero, context.Cause(ctx)
	}
	parking.Park(ctx)
	select {
	case value := <-work:
		return value, nil
	case <-ctx.Done():
		return zero, context.Cause(ctx)
	}
}
