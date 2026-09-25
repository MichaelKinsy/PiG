package harness

import (
	"context"
	"sync"
	"time"
)

// This file ports packages/agent/src/harness/context.ts together with the
// chord Context helpers it re-exports (packages/chord/src/context/index.ts) and
// the pi-telemetry carrier interfaces it depends on
// (packages/telemetry/src/index.ts, noop.ts).
//
// Go's context.Context is the chord Context: values travel as context values
// and the chord abortSignal is the context's Done channel plus its cancellation
// cause. Every harness operation therefore takes a context.Context as its first
// argument (Go convention) where upstream takes a trailing Context.

// Context is the harness invocation context (upstream chord Context).
type Context = context.Context

// ContextKey is a typed, identity-compared context key (upstream ContextKey<T>).
// Two keys created with the same description are distinct.
type ContextKey[T any] struct {
	token *contextKeyToken
}

type contextKeyToken struct{ description string }

// String returns the key description, matching the upstream Symbol description.
func (key ContextKey[T]) String() string {
	if key.token == nil {
		return "anonymous"
	}
	return key.token.description
}

// CreateContextKey returns a fresh typed key (upstream createContextKey).
func CreateContextKey[T any](description string) ContextKey[T] {
	return ContextKey[T]{token: &contextKeyToken{description: description}}
}

// BackgroundContext is the root context with no values or cancellation
// (upstream BACKGROUND_CONTEXT).
func BackgroundContext() Context { return context.Background() }

// TODOContext is the placeholder root context (upstream TODO_CONTEXT).
func TODOContext() Context { return context.TODO() }

// WithContextValue derives a context containing one additional or replaced
// value (upstream withContextValue). The parent is unchanged.
func WithContextValue[T any](parent Context, key ContextKey[T], value T) Context {
	return context.WithValue(parent, key.token, value)
}

// ContextValue returns the value stored under key, reporting whether one is
// present (upstream Context.value, where undefined means absent).
func ContextValue[T any](ctx Context, key ContextKey[T]) (T, bool) {
	value, ok := ctx.Value(key.token).(T)
	return value, ok
}

// abortContext is a harness cancellable context (WithCancel, WithAbortSignal).
// It reproduces the upstream AbortSignal semantics that Go's context package
// lacks for a second cancellation input.
//
// Each context decides its cancellation once, under its own lock, from side
// effect free peeks of its inputs (peek never cancels or notifies anything),
// and records the chosen error and cause together with whether the deciding
// event is happening now or was observed after the fact:
//
//   - An explicit cancel, or a synchronous notification from a harness input
//     that is itself being cancelled now, is a "now" event: any input already
//     cancelled when it is decided happened earlier and wins, in input order.
//   - A standard-library input can only be observed after the fact, through
//     context.AfterFunc or when Err, Done or Value (hence context.Cause)
//     peeks it. Such a "past" event wins over inputs that are merely seen
//     cancelled at the same time, because any earlier harness event would
//     already have decided this context synchronously.
//   - A "past" decision propagates as past to dependants, so an ancestor's
//     late-observed standard cancellation never loses to an in-flight later
//     harness cancellation on another goroutine.
//
// Limitation: when two standard-library inputs are both cancelled before
// either is observed, their relative order is unknowable; the parent's cause
// is preferred.
type abortContext struct {
	std       context.Context // cancel state and values; not a stdlib child of parent
	stdCancel context.CancelCauseFunc
	parent    Context
	upstreams []abortUpstream // parent then signal, each able to cancel

	mu        sync.Mutex
	decided   bool
	err       error // cancellation error recorded with the first cause
	cause     error
	past      bool // the deciding event was observed after the fact
	aborted   bool // listeners notified
	listeners map[*abortListener]struct{}
	releases  []func()
}

// abortUpstream is one input; owner is its harness context, or nil for a
// standard-library context.
type abortUpstream struct {
	ctx   Context
	owner *abortContext
}

type abortListener struct{ fire func(past bool) }

type abortSourceKey struct{}

// abortState is a peeked cancellation.
type abortState struct {
	err, cause error
	past       bool
	ok         bool
}

// Deadline reports the parent's deadline; its expiry cancels through the
// parent upstream.
func (ctx *abortContext) Deadline() (time.Time, bool) { return ctx.parent.Deadline() }

// Done returns the cancellation channel after synchronizing with upstreams.
func (ctx *abortContext) Done() <-chan struct{} {
	ctx.observe()
	return ctx.std.Done()
}

// Err reports cancellation after synchronizing with upstreams. An upstream
// cancellation keeps the upstream's own error (DeadlineExceeded for an expired
// deadline, whatever its cause); an explicit cancel reports Canceled.
func (ctx *abortContext) Err() error {
	ctx.observe()
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	return ctx.err
}

// Value resolves the context's own abort source, otherwise the parent's
// values. It synchronizes with upstreams first so context.Cause observes a
// standard upstream cancellation immediately.
func (ctx *abortContext) Value(key any) any {
	if key == (abortSourceKey{}) {
		return ctx
	}
	ctx.observe()
	return ctx.std.Value(key)
}

func (ctx *abortContext) String() string { return "harness.abortContext" }

// peek reports ctx's cancellation without changing any state: its recorded
// decision, otherwise the first cancelled input in order (always past).
func (ctx *abortContext) peek() abortState {
	ctx.mu.Lock()
	if ctx.decided {
		state := abortState{err: ctx.err, cause: ctx.cause, past: ctx.past, ok: true}
		ctx.mu.Unlock()
		return state
	}
	ctx.mu.Unlock()
	for _, upstream := range ctx.upstreams {
		if state := upstream.peek(); state.ok {
			state.past = true
			return state
		}
	}
	return abortState{}
}

func (upstream abortUpstream) peek() abortState {
	if upstream.owner != nil {
		return upstream.owner.peek()
	}
	if err := upstream.ctx.Err(); err != nil {
		return abortState{err: err, cause: context.Cause(upstream.ctx), past: true, ok: true}
	}
	return abortState{}
}

// decide records state unless already decided and reports whether it did.
// The caller holds ctx.mu.
func (ctx *abortContext) decideLocked(state abortState) bool {
	if ctx.decided {
		return false
	}
	ctx.decided = true
	ctx.err, ctx.cause, ctx.past = state.err, state.cause, state.past
	ctx.stdCancel(state.cause)
	return true
}

// firstCancelledLocked peeks the inputs in order, skipping index skip.
func (ctx *abortContext) firstCancelledLocked(skip int) abortState {
	for index, upstream := range ctx.upstreams {
		if index == skip {
			continue
		}
		if state := upstream.peek(); state.ok {
			state.past = true
			return state
		}
	}
	return abortState{}
}

// observe decides ctx from an input that is already cancelled (a past event),
// making standard-library cancellations visible synchronously.
func (ctx *abortContext) observe() {
	ctx.mu.Lock()
	if ctx.decided {
		ctx.mu.Unlock()
		return
	}
	state := ctx.firstCancelledLocked(-1)
	decided := state.ok && ctx.decideLocked(state)
	ctx.mu.Unlock()
	if decided {
		ctx.fire()
	}
}

// cancel is the explicit cancel function: a now event unless an input was
// already cancelled.
func (ctx *abortContext) cancel(cause error) {
	if cause == nil {
		cause = context.Canceled
	}
	ctx.mu.Lock()
	state := ctx.firstCancelledLocked(-1)
	if !state.ok {
		state = abortState{err: context.Canceled, cause: cause, ok: true}
	}
	decided := ctx.decideLocked(state)
	ctx.mu.Unlock()
	if decided {
		ctx.fire()
	}
}

// cancelFrom handles a notification that input index was cancelled. For a
// now event, inputs already cancelled happened first; for a past event the
// notifying input's (older) cancellation wins.
func (ctx *abortContext) cancelFrom(index int, past bool) {
	ctx.mu.Lock()
	var state abortState
	if !past {
		state = ctx.firstCancelledLocked(index)
	}
	if !state.ok {
		state = ctx.upstreams[index].peek()
		if !state.ok {
			// A standard context reports done only once cancelled; a harness
			// input notifies only after deciding.
			state = abortState{err: context.Canceled, cause: context.Cause(ctx.upstreams[index].ctx), ok: true}
		}
		state.past = past
	}
	decided := ctx.decideLocked(state)
	ctx.mu.Unlock()
	if decided {
		ctx.fire()
	}
}

// sourceOf returns the harness abort context that owns ctx's cancellation, or
// nil when ctx's cancellation is not a harness context (a foreign cancel layer
// above one, or WithoutAbortSignal). Value layers keep the owner's Done
// channel, so they resolve to the same owner.
func sourceOf(ctx Context) *abortContext {
	source, _ := ctx.Value(abortSourceKey{}).(*abortContext)
	if source == nil {
		return nil
	}
	done := ctx.Done()
	if done == nil || source.std.Done() != done {
		return nil
	}
	return source
}

// subscribe registers fire to run synchronously when ctx aborts. It returns
// false, without registering, when ctx already aborted.
func (ctx *abortContext) subscribe(fire func(past bool)) (func(), bool) {
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	if ctx.aborted {
		return nil, false
	}
	listener := &abortListener{fire: fire}
	if ctx.listeners == nil {
		ctx.listeners = map[*abortListener]struct{}{}
	}
	ctx.listeners[listener] = struct{}{}
	return func() {
		ctx.mu.Lock()
		defer ctx.mu.Unlock()
		delete(ctx.listeners, listener)
	}, true
}

func (ctx *abortContext) addRelease(release func()) {
	ctx.mu.Lock()
	if !ctx.aborted {
		ctx.releases = append(ctx.releases, release)
		ctx.mu.Unlock()
		return
	}
	ctx.mu.Unlock()
	release()
}

// fire notifies listeners once, passing whether the decision was a past
// event, then releases ctx's own registrations on its upstreams.
func (ctx *abortContext) fire() {
	ctx.mu.Lock()
	if ctx.aborted || !ctx.decided {
		ctx.mu.Unlock()
		return
	}
	ctx.aborted = true
	past := ctx.past
	listeners := ctx.listeners
	releases := ctx.releases
	ctx.listeners, ctx.releases = nil, nil
	ctx.mu.Unlock()
	for listener := range listeners {
		listener.fire(past)
	}
	for _, release := range releases {
		release()
	}
}

// newAbortable derives a harness cancellable context from parent and an
// optional extra signal.
func newAbortable(parent Context, signal Context) *abortContext {
	std, stdCancel := context.WithCancelCause(context.WithoutCancel(parent))
	ctx := &abortContext{std: std, stdCancel: stdCancel, parent: parent}
	for _, upstream := range []Context{parent, signal} {
		if upstream != nil && upstream.Done() != nil {
			ctx.upstreams = append(ctx.upstreams, abortUpstream{ctx: upstream, owner: sourceOf(upstream)})
		}
	}
	// Like AbortSignal.any([parent, signal]), an input already aborted at
	// construction wins in input order.
	ctx.observe()
	for index := range ctx.upstreams {
		if ctx.follow(index) {
			break
		}
	}
	return ctx
}

// follow cancels ctx when input index aborts: synchronously for a harness
// input, through AfterFunc (plus observation peeks) otherwise. It reports
// whether ctx is already cancelled.
func (ctx *abortContext) follow(index int) bool {
	upstream := ctx.upstreams[index]
	if upstream.owner != nil {
		unsubscribe, ok := upstream.owner.subscribe(func(past bool) { ctx.cancelFrom(index, past) })
		if !ok {
			ctx.observe()
			return true
		}
		ctx.addRelease(unsubscribe)
		return false
	}
	stop := context.AfterFunc(upstream.ctx, func() { ctx.cancelFrom(index, true) })
	ctx.addRelease(func() { stop() })
	return false
}

// WithAbortSignal derives a context cancelled by either the parent or the
// supplied signal (upstream withAbortSignal, which combines the two with
// AbortSignal.any). The signal is any context whose Done channel represents an
// abort, such as execution.Gate.Signal(). The derived context keeps the first
// cause: the parent's or the signal's, whichever aborted first. When the signal
// and parent are harness contexts (created by WithCancel or WithAbortSignal,
// possibly under value layers) the abort is visible synchronously, like
// AbortSignal.any; a signal already aborted aborts the derived context before
// this function returns. Other contexts propagate promptly through
// context.AfterFunc. The parent context remains unchanged. A nil signal returns
// the parent unchanged.
func WithAbortSignal(parent Context, signal Context) Context {
	if signal == nil {
		return parent
	}
	return newAbortable(parent, signal)
}

// WithoutAbortSignal derives a context retaining all values except caller
// cancellation (upstream withoutAbortSignal). Intended for mandatory cleanup
// and for drive passes that must outlive the caller's wait.
func WithoutAbortSignal(parent Context) Context {
	return context.WithoutCancel(parent)
}

// WithCancel derives an independently cancellable child context (upstream
// withCancel). The cancel function records its cause as the abort reason; a nil
// cause records context.Canceled. Cancellation is visible synchronously to
// contexts combined with it through WithAbortSignal.
func WithCancel(parent Context) (Context, context.CancelCauseFunc) {
	ctx := newAbortable(parent, nil)
	return ctx, ctx.cancel
}

// AbortError returns the error representing a context's abort reason (upstream
// abortError): the cancellation cause, which is context.Canceled or
// context.DeadlineExceeded when no explicit cause was supplied.
func AbortError(ctx Context) error {
	return context.Cause(ctx)
}

// AwaitWithContext observes done until it is closed or ctx is cancelled
// (upstream awaitWithContext). Cancellation returns only this waiter's abort
// error; it does not cancel the underlying work. If ctx is already cancelled
// the abort error is returned without observing done, like the upstream
// synchronous rejection. The underlying work's own result is carried by the
// caller (for example through a variable written before done is closed).
func AwaitWithContext(ctx Context, done <-chan struct{}) error {
	if ctx.Err() != nil {
		return AbortError(ctx)
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return AbortError(ctx)
	}
}

// AttributeValue is a telemetry attribute value: string, number (int, int64 or
// float64), bool, or a slice of one of those (upstream AttributeValue).
type AttributeValue = any

// SpanAttributes maps attribute names to values (upstream SpanAttributes).
type SpanAttributes map[string]AttributeValue

// SpanOptions names a span and supplies its start attributes (upstream
// SpanOptions).
type SpanOptions struct {
	Name       string
	Attributes SpanAttributes
}

// SpanStatusCode is "ok" or "error".
type SpanStatusCode string

// Span status codes.
const (
	SpanStatusCodeOK    SpanStatusCode = "ok"
	SpanStatusCodeError SpanStatusCode = "error"
)

// SpanStatusError carries optional error details for an error status.
type SpanStatusError struct {
	Name    string
	Message string
}

// SpanStatus is a span's final status (upstream SpanStatus).
type SpanStatus struct {
	Status SpanStatusCode
	// Error is optional and only meaningful when Status is SpanStatusCodeError.
	Error *SpanStatusError
}

// TelemetryContext starts child spans (upstream pi-telemetry TelemetryContext).
// Upstream startSpan returns the callback's value; Go callers capture results
// in the callback closure and StartSpan returns the callback's error.
type TelemetryContext interface {
	StartSpan(options SpanOptions, callback func(span TelemetrySpan) error) error
}

// TelemetrySpan is an active span that can itself parent children (upstream
// TelemetrySpan).
type TelemetrySpan interface {
	TelemetryContext
	AddEvent(name string, attributes SpanAttributes)
	SetAttributes(attributes SpanAttributes)
	SetStatus(status SpanStatus)
}

type noopTelemetrySpan struct{}

func (noopTelemetrySpan) StartSpan(_ SpanOptions, callback func(span TelemetrySpan) error) error {
	return callback(NoopTelemetryContext)
}
func (noopTelemetrySpan) AddEvent(string, SpanAttributes) {}
func (noopTelemetrySpan) SetAttributes(SpanAttributes)    {}
func (noopTelemetrySpan) SetStatus(SpanStatus)            {}

// NoopTelemetryContext is the shared telemetry context used when an
// application does not provide one (upstream NOOP_TELEMETRY_CONTEXT). It is
// also a span, so children started from it receive it again.
var NoopTelemetryContext TelemetrySpan = noopTelemetrySpan{}

var telemetryContextKey = CreateContextKey[TelemetryContext]("pi.telemetryContext")

// GetTelemetryContext returns the telemetry parent attached to a context, or
// the shared no-op parent (upstream getTelemetryContext).
func GetTelemetryContext(ctx Context) TelemetryContext {
	if telemetry, ok := ContextValue(ctx, telemetryContextKey); ok && telemetry != nil {
		return telemetry
	}
	return NoopTelemetryContext
}

// WithTelemetryContext derives a context whose telemetry children use the
// supplied parent or active span (upstream withTelemetryContext).
func WithTelemetryContext(ctx Context, telemetry TelemetryContext) Context {
	return WithContextValue(ctx, telemetryContextKey, telemetry)
}
