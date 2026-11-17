// Package chordctx ports packages/chord/src/context/index.ts (`@earendil-works/chord/context`) onto context.Context. A chord Context is a Go context: an abort signal is cancellation, with the abort reason carried as the cancellation cause, and `toString` is the context's String method, which prints Pi's text.
// Background and the placeholder root are Pi's BACKGROUND_CONTEXT and TODO_CONTEXT roots; contexts derived from them by this package print as Pi's contexts do. A context derived from another root, such as context.Background, prints that root's own text first.
package chordctx

import (
	"context"
	"fmt"
	"sync"
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
//
// Pi's signal of such a context is an `AbortSignal.any` composite of its sources: the parent's signal, the signal given to withAbortSignal, and the controller withCancel makes. An abort of a source marks every composite derived from it aborted before any listener runs, then runs the source's own listeners, then each composite's in the order the composites were created. Here the sources that are contexts of this package are linked: the cancel function of a source aborts the contexts derived from it before it returns (abortLinked), then runs the listeners (dispatch), so no context derived from a source of this package is ever seen live after the source's cancel returned. A source of another package cannot be linked: context.AfterFunc is the only hook it offers, and it runs on a goroutine of its own.
type abortContext struct {
	context.Context
	parent context.Context
	// detached is set on a WithoutAbortSignal context, whose embedded context is stdlib's WithoutCancel.
	detached bool
	// own is set on a context that has a cancel function of its own (Pi's controller): WithCancel and WithAbortSignalCancel;
	// aborted is set, under mu, once the call that owns the cancel function has aborted the context and what is derived from it.
	own, aborted bool
	// cancelCause ends the embedded context; nil on a detached one.
	cancelCause context.CancelCauseFunc
	// created numbers the contexts of this package in the order they were derived: Pi's composites run their listeners in
	// the order they were created.
	created uint64
	// order is the place of this context's own cancel in the order of aborts (see AbortOrder); 0 until the first call of the
	// cancel function claimed it (claim).
	order atomic.Uint64
	// listeners are the OnAbort listeners of the cancel functions that abort this context, guarded by mu; a cancel function
	// runs and clears them. links are the contexts derived from a signal this context's cancel can abort, which the cancel
	// function aborts first (abortLinked), guarded by mu.
	mu        sync.Mutex
	listeners map[*abortListener]struct{}
	links     map[*abortContext]struct{}
	// settled is closed, under mu, when aborted is set; the first call that has to wait for that makes it (awaitAbort).
	settled chan struct{}
	// signaled is set on a context derived from a signal (WithAbortSignal, WithAbortSignalCancel) before it is linked, and
	// unchanged after; nil on every other, which is most of them, so they do not carry what only a signal needs.
	signaled *signalLink
}

// signalLink is what a context derived from a signal holds besides what every context of this package does.
type signalLink struct {
	// signal is the other source of the context.
	signal context.Context
	// linkedTo are the contexts whose links hold the context, and stopSignal stops the context.AfterFunc of a signal that is
	// not of this package (nil when every source is).
	linkedTo   []*abortContext
	stopSignal func() bool
}

// linkedContext is a context of this package with the link of its signal, made as one allocation.
type linkedContext struct {
	abortContext
	link signalLink
}

// aborts numbers the aborts of the contexts WithCancel makes, in the order their cancel functions ran.
var aborts atomic.Uint64

// claim makes the caller the owner of this context's own cancel, and reports whether it is: true for the first call of the
// cancel function, which then aborts the context and runs its listeners, false for every call after it, repeated or running at
// once on another goroutine. The claim is made before the context is cancelled, so a second call can never find the links and
// the listeners of an abort that is in progress and take them from the call that aborts: an `abort()` of an aborted signal runs
// nothing.
func (c *abortContext) claim() bool {
	return c.order.Load() == 0 && c.order.CompareAndSwap(0, aborts.Add(1))
}

// derivations numbers the contexts of this package as they are created (abortContext.created).
var derivations atomic.Uint64

// AbortOrder is when ctx was aborted relative to other contexts of this package: the number of the earliest abort that
// ended it (its own cancel, or the one of a parent or signal it derives from), 0 for a context not aborted by this
// package's cancel functions. The walk through the parents ends at a WithoutAbortSignal context: the aborts behind it do not
// end what is derived from it. AbortSignal.any takes the reason of the source that aborted first; a context derived from two
// sources of this package takes the cause of the one whose cancel function reached it first, and two cancel functions that
// run at once on two goroutines reach it in any order, so the one that decides the reason compares AbortOrder of the
// sources instead.
func AbortOrder(ctx context.Context) uint64 {
	var earliest uint64
	for ctx != nil {
		if c, ok := ctx.(*signalContext); ok {
			// Its abort is its signal's.
			if order := AbortOrder(c.Context); order != 0 && (earliest == 0 || order < earliest) {
				earliest = order
			}
			return earliest
		}
		c, ok := ctx.(*abortContext)
		if !ok {
			return earliest
		}
		if c.detached {
			// Nothing behind a WithoutAbortSignal context aborts it: Pi's withoutAbortSignal leaves the context no abort signal
			// (context/index.ts:118-120), so what is derived from it is aborted by its own cancel and its other sources alone.
			return earliest
		}
		if order := c.order.Load(); order != 0 && (earliest == 0 || order < earliest) {
			earliest = order
		}
		if c.signaled != nil {
			if order := AbortOrder(c.signaled.signal); order != 0 && (earliest == 0 || order < earliest) {
				earliest = order
			}
		}
		ctx = c.parent
	}
	return earliest
}

func (c *abortContext) String() string { return fmt.Sprint(c.parent) + ".WithValue(chord.abortSignal)" }

// Value is the embedded context's, except that a key of this package (a Token) a WithoutAbortSignal context looks up in its
// parent directly: stdlib's WithoutCancel context is a value type whose Value method boxes its receiver again on every
// call, an allocation per lookup (the telemetry parent of a ctx is one) that reaches it, and it keeps nothing under a key of
// this package that the parent does not.
func (c *abortContext) Value(key any) any {
	if _, ok := key.(Token); ok && c.detached {
		return c.parent.Value(key)
	}
	return c.Context.Value(key)
}

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
// The result is cancelled inside the cancel function that aborts a source that is a context of this package, before that function returns and before any listener of the abort runs, as AbortSignal.any marks its composite aborted when a source aborts. A source of another package (a deadline, a context.WithCancel of the standard library) cannot be hooked: a signal of that kind cancels the result on a goroutine of its own (context.AfterFunc), and OnAbort reports it as not exact.
// A parent with no cancellation (no Done channel) leaves signal alone to cancel: Pi's `withAbortSignal` then uses signal itself, with no listener, and so does the result here (signalContext), which registers nothing on signal, so a signal that outlives its derived contexts (an attempt's, a reference's) keeps nothing of them.
func WithAbortSignal(parent, signal context.Context) context.Context {
	if parent.Done() == nil {
		return &signalContext{Context: signal, parent: parent}
	}
	return derive(parent, signal, false)
}

// WithAbortSignalCancel derives a context cancelled by the parent, by signal, or by cancel: Pi's `withCancel(withAbortSignal(signal, parent))` as one context, so that cancel also unlinks it from signal. A caller that derives per operation and ends the operation before either source aborts calls cancel (a nil cause records context.Canceled), as it would call the one WithCancel returns; WithAbortSignal has none and keeps its link until a source aborts. The result is cancelled as WithAbortSignal's is, and its own cancel runs the listeners registered on it as WithCancel's does.
func WithAbortSignalCancel(parent, signal context.Context) (ctx context.Context, cancel func(cause error)) {
	child := derive(parent, signal, true)
	return child, child.cancel
}

// derive makes a context of this package under parent: with its own cancel function (own), and cancelled also by signal when
// that is not nil.
func derive(parent, signal context.Context, own bool) *abortContext {
	inner, cancelCause := context.WithCancelCause(parent)
	var c *abortContext
	if signal != nil {
		linked := &linkedContext{link: signalLink{signal: signal}}
		c = &linked.abortContext
		c.signaled = &linked.link
	} else {
		c = &abortContext{}
	}
	c.Context, c.parent, c.cancelCause, c.own, c.created = inner, parent, cancelCause, own, derivations.Add(1)
	if signal != nil {
		c.link()
	}
	return c
}

// link makes the cancel function of every source of c that is a context of this package abort c, and hooks a source of
// another package with context.AfterFunc. A source is registered once its sources are known, last, so that a cancel function
// that finds c in its links cancels a context that is complete: the registration and the check that the source is live are
// one step under the source's mu, as OnAbort's are, so a cancel in between finds c and one before it is seen here.
func (c *abortContext) link() {
	signal := c.signaled.signal
	if signal.Err() != nil {
		c.cancelCause(context.Cause(signal))
		return
	}
	sources, fromParent := abortSources(c.parent, nil)
	sources, fromSignal := abortSources(signal, sources)
	c.signaled.linkedTo = sources
	if !fromSignal {
		c.signaled.stopSignal = context.AfterFunc(signal, func() {
			c.cancelCause(context.Cause(signal))
			c.unlinkSources()
		})
	}
	if !fromParent || !fromSignal {
		// c may be ended by a cancel no cancel function of this package runs (the standard library cancelling a parent): nothing
		// else would take it out of the links of its sources.
		context.AfterFunc(c.Context, c.unlinkSources)
	}
	for _, source := range sources {
		source.mu.Lock()
		if source.Err() != nil {
			source.mu.Unlock()
			c.abortFrom(context.Cause(source.Context))
			return
		}
		if source.links == nil {
			source.links = map[*abortContext]struct{}{}
		}
		source.links[c] = struct{}{}
		source.mu.Unlock()
	}
}

// abortFrom is the abort of a source of c: it ends c with the source's cause and releases what c holds.
func (c *abortContext) abortFrom(cause error) {
	c.cancelCause(cause)
	c.unlink()
}

// unlink releases c's registration on every source and the hook on a signal of another package.
func (c *abortContext) unlink() {
	if c.signaled == nil {
		return
	}
	c.unlinkSources()
	if c.signaled.stopSignal != nil {
		c.signaled.stopSignal()
	}
}

func (c *abortContext) unlinkSources() {
	for _, source := range c.signaled.linkedTo {
		source.mu.Lock()
		delete(source.links, c)
		if len(source.links) == 0 {
			source.links = nil
		}
		source.mu.Unlock()
	}
}

// abortLinked aborts the contexts derived from a signal this context's cancel aborts (WithAbortSignal), with this context's
// cause. It runs inside the cancel function after the context itself is cancelled and before the listeners run: Pi marks
// every composite of a source aborted before it runs any listener.
func (c *abortContext) abortLinked() {
	c.mu.Lock()
	linked := c.links
	c.links = nil
	c.mu.Unlock()
	if len(linked) == 0 {
		return
	}
	cause := context.Cause(c.Context)
	for derived := range linked {
		derived.abortFrom(cause)
	}
}

// cancel is the cancel function of a context with its own: the first call aborts the context, then everything derived from it,
// then runs the listeners of the abort. A call after it, repeated or running at once on another goroutine, does none of that
// and keeps the first cause, as `abort()` of an aborted signal does nothing; it returns once the first has aborted the context
// and what is derived from it (awaitAbort), without waiting for the listeners, which are the first call's to run.
func (c *abortContext) cancel(cause error) {
	if !c.claim() {
		c.awaitAbort()
		return
	}
	c.cancelCause(cause)
	c.unlink()
	c.abortLinked()
	c.dispatch()
}

// signalContext is a context whose abort signal is another context's: the Done channel, the error, the deadline and the
// cause are signal's, as Pi's `withAbortSignal` with no parent signal is `withContextValue(ABORT_SIGNAL_CONTEXT_KEY, signal,
// context)`. Its values are parent's (a parent with no cancellation); a key parent does not know the signal answers, which is
// how context.Cause reaches the signal's cancellation record. It listens to nothing.
type signalContext struct {
	// Context is the signal.
	context.Context
	parent context.Context
}

func (c *signalContext) Value(key any) any {
	if value := c.parent.Value(key); value != nil {
		return value
	}
	return c.Context.Value(key)
}

func (c *signalContext) String() string {
	return fmt.Sprint(c.parent) + ".WithValue(chord.abortSignal)"
}

// WithoutAbortSignal derives a context that keeps every value but ignores caller cancellation. It is for mandatory cleanup only.
func WithoutAbortSignal(parent context.Context) context.Context {
	return &abortContext{Context: context.WithoutCancel(parent), parent: parent, detached: true}
}

// WithCancel derives an independently cancellable child. cancel records cause, and a nil cause records context.Canceled.
func WithCancel(parent context.Context) (ctx context.Context, cancel func(cause error)) {
	child := derive(parent, nil, true)
	return child, child.cancel
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

// Suspend is the first suspension of a method handler: Pi runs an async handler synchronously, inside the section that admits its call, up to its first `await`, and the rest of that section waits for it. A Go handler is blocking code that runs as a coroutine of the section's goroutine, and the section waits until it returns or suspends. The host primitives that wait (Get, Call, Sleep, UntilAborted, transactions, Await, Receive) suspend the handler themselves; call Suspend with the handler's ctx, or a context derived from it, right before waiting for anything else (a channel, a condition, I/O that another goroutine of the host's section must not outwait). Code that waits without suspending is a synchronous block of the section, as a synchronous loop is in Pi: the host's other work does not run until it ends. Suspend of a context that carries no handler does nothing, and a second call does nothing. Only the handler's own goroutine suspends it: a goroutine the handler started leaves the handler's synchronous part to the handler's own next wait, whatever it waits for itself, as an `await` inside async work that Pi's handler started and did not await does.
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
	outcome, err := parking.Recv(ctx, work)
	if err != nil {
		return zero, err
	}
	return outcome.Value, outcome.Err
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
	return parking.Recv(ctx, work)
}
