package harness

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"testing"
	"time"
)

type spanRecord struct {
	id, parent int
	name       string
}

type memoryTelemetry struct {
	mu    sync.Mutex
	spans []spanRecord
}

type memorySpan struct {
	telemetry *memoryTelemetry
	id        int
}

func (telemetry *memoryTelemetry) start(parent int, options SpanOptions, callback func(TelemetrySpan) error) error {
	telemetry.mu.Lock()
	id := len(telemetry.spans) + 1
	telemetry.spans = append(telemetry.spans, spanRecord{id: id, parent: parent, name: options.Name})
	telemetry.mu.Unlock()
	return callback(&memorySpan{telemetry: telemetry, id: id})
}

func (telemetry *memoryTelemetry) StartSpan(options SpanOptions, callback func(TelemetrySpan) error) error {
	return telemetry.start(0, options, callback)
}

func (span *memorySpan) StartSpan(options SpanOptions, callback func(TelemetrySpan) error) error {
	return span.telemetry.start(span.id, options, callback)
}
func (*memorySpan) AddEvent(string, SpanAttributes) {}
func (*memorySpan) SetAttributes(SpanAttributes)    {}
func (*memorySpan) SetStatus(SpanStatus)            {}

// Upstream context.test.ts: uses no-op telemetry when none is attached.
func TestGetTelemetryContextDefaultsToNoop(t *testing.T) {
	if GetTelemetryContext(BackgroundContext()) != NoopTelemetryContext {
		t.Fatal("background context telemetry is not the no-op parent")
	}
	if GetTelemetryContext(TODOContext()) != NoopTelemetryContext {
		t.Fatal("TODO context telemetry is not the no-op parent")
	}
	called := false
	failure := errors.New("callback failed")
	err := NoopTelemetryContext.StartSpan(SpanOptions{Name: "noop"}, func(span TelemetrySpan) error {
		called = span == NoopTelemetryContext
		span.SetAttributes(SpanAttributes{"a": 1})
		span.SetStatus(SpanStatus{Status: SpanStatusCodeError})
		span.AddEvent("e", nil)
		return failure
	})
	if !called || err != failure { //nolint:errorlint // upstream preserves the exact rejection.
		t.Fatalf("noop span called=%v err=%v", called, err)
	}
}

// Upstream context.test.ts: carries telemetry as an ordinary context value.
func TestWithTelemetryContextCarriesParent(t *testing.T) {
	telemetry := &memoryTelemetry{}
	ctx := WithTelemetryContext(BackgroundContext(), telemetry)
	err := GetTelemetryContext(ctx).StartSpan(SpanOptions{Name: "parent"}, func(span TelemetrySpan) error {
		child := WithTelemetryContext(ctx, span)
		return GetTelemetryContext(child).StartSpan(SpanOptions{Name: "child"}, func(TelemetrySpan) error { return nil })
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(telemetry.spans) != 2 || telemetry.spans[0].name != "parent" || telemetry.spans[1].name != "child" || telemetry.spans[1].parent != telemetry.spans[0].id {
		t.Fatalf("spans = %+v", telemetry.spans)
	}
	if GetTelemetryContext(BackgroundContext()) != NoopTelemetryContext {
		t.Fatal("deriving a telemetry context changed its parent")
	}
}

func TestContextKeysAreIdentityCompared(t *testing.T) {
	first := CreateContextKey[string]("same")
	second := CreateContextKey[string]("same")
	ctx := WithContextValue(BackgroundContext(), first, "one")
	if value, ok := ContextValue(ctx, first); !ok || value != "one" {
		t.Fatalf("first = %q, %v", value, ok)
	}
	if _, ok := ContextValue(ctx, second); ok {
		t.Fatal("a distinct key with the same description read the value")
	}
	replaced := WithContextValue(ctx, first, "two")
	if value, _ := ContextValue(replaced, first); value != "two" {
		t.Fatalf("replaced = %q", value)
	}
	if value, _ := ContextValue(ctx, first); value != "one" {
		t.Fatalf("parent changed to %q", value)
	}
	if first.String() != "same" {
		t.Fatalf("description = %q", first.String())
	}
}

func waitDone(t *testing.T, ctx Context) {
	t.Helper()
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("context was not cancelled")
	}
}

func TestWithAbortSignalCombinesParentAndSignal(t *testing.T) {
	key := CreateContextKey[int]("value")
	parent, cancelParent := WithCancel(WithContextValue(BackgroundContext(), key, 7))
	signal, abort := WithCancel(BackgroundContext())
	combined := WithAbortSignal(parent, signal)
	if value, _ := ContextValue(combined, key); value != 7 {
		t.Fatalf("value = %d", value)
	}
	reason := errors.New("signal aborted")
	abort(reason)
	waitDone(t, combined)
	if cause := context.Cause(combined); cause != reason { //nolint:errorlint // abort reason identity.
		t.Fatalf("cause = %v, want %v", cause, reason)
	}
	if parent.Err() != nil {
		t.Fatal("aborting the signal cancelled the parent")
	}

	fresh, freshAbort := WithCancel(BackgroundContext())
	defer freshAbort(nil)
	fromParent := WithAbortSignal(parent, fresh)
	parentReason := errors.New("parent aborted")
	cancelParent(parentReason)
	waitDone(t, fromParent)
	if cause := AbortError(fromParent); cause != parentReason { //nolint:errorlint // abort reason identity.
		t.Fatalf("cause = %v, want %v", cause, parentReason)
	}
	if fresh.Err() != nil {
		t.Fatal("parent cancellation aborted the signal")
	}
}

func TestWithAbortSignalPreAbortedIsSynchronous(t *testing.T) {
	signal, abort := WithCancel(BackgroundContext())
	reason := errors.New("already aborted")
	abort(reason)
	combined := WithAbortSignal(BackgroundContext(), signal)
	if combined.Err() == nil || AbortError(combined) != reason { //nolint:errorlint // abort reason identity.
		t.Fatalf("combined err=%v cause=%v, want immediate %v", combined.Err(), AbortError(combined), reason)
	}
	if WithAbortSignal(BackgroundContext(), nil) != BackgroundContext() {
		t.Fatal("nil signal did not return the parent")
	}
}

func TestWithoutAbortSignalKeepsValuesOnly(t *testing.T) {
	key := CreateContextKey[string]("kept")
	parent, cancel := WithCancel(WithTelemetryContext(WithContextValue(BackgroundContext(), key, "value"), NoopTelemetryContext))
	cancel(errors.New("caller gone"))
	detached := WithoutAbortSignal(parent)
	if detached.Err() != nil {
		t.Fatalf("detached context is cancelled: %v", detached.Err())
	}
	if value, _ := ContextValue(detached, key); value != "value" {
		t.Fatalf("value = %q", value)
	}
}

func TestAwaitWithContext(t *testing.T) {
	done := make(chan struct{})
	close(done)
	if err := AwaitWithContext(BackgroundContext(), done); err != nil {
		t.Fatalf("settled work = %v", err)
	}
	ctx, cancel := WithCancel(BackgroundContext())
	reason := errors.New("waiter cancelled")
	pending := make(chan struct{})
	result := make(chan error, 1)
	go func() { result <- AwaitWithContext(ctx, pending) }()
	cancel(reason)
	if err := <-result; err != reason { //nolint:errorlint // abort reason identity.
		t.Fatalf("cancelled wait = %v, want %v", err, reason)
	}
	// An already-cancelled waiter rejects even when the work has settled.
	if err := AwaitWithContext(ctx, done); err != reason { //nolint:errorlint // abort reason identity.
		t.Fatalf("pre-cancelled wait = %v, want %v", err, reason)
	}
	select {
	case <-pending:
		t.Fatal("cancelling the waiter settled the work")
	default:
	}
}

// Review P1: upstream AbortSignal.any aborts the combined signal synchronously
// and keeps the first reason, even when the parent is cancelled afterwards.
func TestWithAbortSignalSignalAbortIsSynchronousAndFirstReasonWins(t *testing.T) {
	parent, cancelParent := WithCancel(BackgroundContext())
	signal, abortSignal := WithCancel(BackgroundContext())
	combined := WithAbortSignal(parent, signal)
	first := errors.New("signal first")
	abortSignal(first)
	if combined.Err() == nil {
		t.Fatal("combined context not aborted immediately after the signal")
	}
	select {
	case <-combined.Done():
	default:
		t.Fatal("combined Done not closed immediately after the signal")
	}
	cancelParent(errors.New("parent second"))
	if cause := AbortError(combined); cause != first { //nolint:errorlint // abort reason identity.
		t.Fatalf("cause = %v, want %v", cause, first)
	}
}

// Cancellation reaches contexts combined from derived signals synchronously,
// including through value layers and parent chains.
func TestWithAbortSignalDerivedSignalsAreSynchronous(t *testing.T) {
	root, cancelRoot := WithCancel(BackgroundContext())
	key := CreateContextKey[int]("k")
	signal := WithContextValue(WithAbortSignal(WithContextValue(root, key, 1), BackgroundContext()), key, 2)
	child, cancelChild := WithCancel(signal)
	defer cancelChild(nil)
	combined := WithAbortSignal(BackgroundContext(), child)
	nested := WithAbortSignal(WithContextValue(BackgroundContext(), key, 3), combined)
	reason := errors.New("root aborted")
	cancelRoot(reason)
	for name, ctx := range map[string]Context{"combined": combined, "nested": nested} {
		if AbortError(ctx) != reason { //nolint:errorlint // abort reason identity.
			t.Fatalf("%s cause = %v, want immediate %v", name, AbortError(ctx), reason)
		}
	}
}

// Review-2 P1: a standard Go signal cancelled first keeps its reason and is
// visible immediately, even when the harness parent is cancelled afterwards.
func TestStandardSignalKeepsFirstReason(t *testing.T) {
	signal, cancelSignal := context.WithCancelCause(context.Background())
	parent, cancelParent := WithCancel(BackgroundContext())
	combined := WithAbortSignal(parent, signal)
	first := errors.New("signal first")
	cancelSignal(first)
	if combined.Err() == nil {
		t.Fatal("combined not aborted immediately after a standard signal")
	}
	cancelParent(errors.New("parent second"))
	if cause := context.Cause(combined); cause != first { //nolint:errorlint // abort reason identity.
		t.Fatalf("cause = %v, want %v", cause, first)
	}
}

// Without observing in between, the earlier standard-context cancellation
// still wins over a later harness cancellation.
func TestStandardSignalUnobservedKeepsFirstReason(t *testing.T) {
	signal, cancelSignal := context.WithCancelCause(context.Background())
	parent, cancelParent := WithCancel(BackgroundContext())
	combined := WithAbortSignal(parent, signal)
	first := errors.New("signal first")
	cancelSignal(first)
	cancelParent(errors.New("parent second"))
	if cause := context.Cause(combined); cause != first { //nolint:errorlint // abort reason identity.
		t.Fatalf("cause = %v, want %v", cause, first)
	}
}

// Review-2 P1: a harness signal whose ancestor is a standard context.
func TestHarnessSignalWithStandardParentKeepsFirstReason(t *testing.T) {
	root, cancelRoot := context.WithCancelCause(context.Background())
	signal, cancelSignal := WithCancel(root)
	defer cancelSignal(nil)
	parent, cancelParent := WithCancel(BackgroundContext())
	combined := WithAbortSignal(parent, signal)
	first := errors.New("root first")
	cancelRoot(first)
	select {
	case <-combined.Done():
	default:
		t.Fatal("combined Done not closed immediately after the standard ancestor")
	}
	cancelParent(errors.New("parent second"))
	if cause := context.Cause(combined); cause != first { //nolint:errorlint // abort reason identity.
		t.Fatalf("cause = %v, want %v", cause, first)
	}
	if cause := context.Cause(signal); cause != first { //nolint:errorlint // abort reason identity.
		t.Fatalf("signal cause = %v, want %v", cause, first)
	}
}

// A standard deadline on the parent still reports DeadlineExceeded.
func TestHarnessContextKeepsParentDeadline(t *testing.T) {
	parent, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	ctx, cancelCtx := WithCancel(parent)
	defer cancelCtx(nil)
	if _, ok := ctx.Deadline(); !ok {
		t.Fatal("deadline not inherited")
	}
	<-ctx.Done()
	if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded", ctx.Err())
	}
}

func singleProc(t *testing.T) {
	t.Helper()
	previous := runtime.GOMAXPROCS(1)
	t.Cleanup(func() { runtime.GOMAXPROCS(previous) })
}

// Review-3 P1: an unobserved standard ancestor of the harness parent was
// cancelled first; a later harness signal cancellation must not win through
// re-entrant observation.
func TestStandardParentAncestorKeepsFirstReasonUnobserved(t *testing.T) {
	singleProc(t)
	root, cancelRoot := context.WithCancelCause(context.Background())
	parent, cancelParent := WithCancel(root)
	defer cancelParent(nil)
	signal, cancelSignal := WithCancel(BackgroundContext())
	combined := WithAbortSignal(parent, signal)
	first := errors.New("root first")
	cancelRoot(first)
	cancelSignal(errors.New("signal second"))
	if cause := context.Cause(combined); cause != first { //nolint:errorlint // abort reason identity.
		t.Fatalf("cause = %v, want %v", cause, first)
	}
}

// Review-3 P2: AbortSignal.any([parent, signal]) prefers the parent when both
// inputs are already aborted, regardless of their cancellation order.
func TestPreCancelledInputsPreferParent(t *testing.T) {
	factories := map[string]func() (Context, context.CancelCauseFunc){
		"harness":  func() (Context, context.CancelCauseFunc) { return WithCancel(BackgroundContext()) },
		"standard": func() (Context, context.CancelCauseFunc) { return context.WithCancelCause(context.Background()) },
	}
	for name, factory := range factories {
		for _, parentFirst := range []bool{true, false} {
			parent, cancelParent := factory()
			signal, cancelSignal := factory()
			parentReason, signalReason := errors.New("parent"), errors.New("signal")
			if parentFirst {
				cancelParent(parentReason)
				cancelSignal(signalReason)
			} else {
				cancelSignal(signalReason)
				cancelParent(parentReason)
			}
			if cause := context.Cause(WithAbortSignal(parent, signal)); cause != parentReason { //nolint:errorlint // abort reason identity.
				t.Fatalf("%s parentFirst=%v: cause = %v, want parent", name, parentFirst, cause)
			}
		}
	}
}

// Review-3 P2: an expired deadline with a custom cause keeps Err ==
// DeadlineExceeded and its cause.
func TestDeadlineCauseKeepsDeadlineClassification(t *testing.T) {
	cause := errors.New("deadline cause")
	parent, cancel := context.WithDeadlineCause(context.Background(), time.Now().Add(-time.Second), cause)
	defer cancel()
	ctx, cancelCtx := WithCancel(parent)
	defer cancelCtx(nil)
	if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded", ctx.Err())
	}
	if got := context.Cause(ctx); got != cause { //nolint:errorlint // cause identity.
		t.Fatalf("cause = %v, want %v", got, cause)
	}
	combined := WithAbortSignal(BackgroundContext(), parent)
	if !errors.Is(combined.Err(), context.DeadlineExceeded) || context.Cause(combined) != cause { //nolint:errorlint // cause identity.
		t.Fatalf("combined err=%v cause=%v", combined.Err(), context.Cause(combined))
	}
	// An explicit cancel whose cause happens to be DeadlineExceeded is still a
	// cancellation.
	explicit, cancelExplicit := WithCancel(BackgroundContext())
	cancelExplicit(context.DeadlineExceeded)
	if explicit.Err() != context.Canceled { //nolint:errorlint // exact sentinel.
		t.Fatalf("explicit err = %v, want Canceled", explicit.Err())
	}
}

// Review-4 P1: the parent-ancestor arrangement keeps the first reason while
// the standard ancestor's AfterFunc notification runs concurrently.
func TestStandardParentAncestorConcurrentFirstReason(t *testing.T) {
	previous := runtime.GOMAXPROCS(2)
	t.Cleanup(func() { runtime.GOMAXPROCS(previous) })
	for iteration := range 20000 {
		root, cancelRoot := context.WithCancelCause(context.Background())
		parent, cancelParent := WithCancel(root)
		signal, cancelSignal := WithCancel(BackgroundContext())
		combined := WithAbortSignal(parent, signal)
		first := errors.New("root first")
		cancelRoot(first)
		cancelSignal(errors.New("signal second"))
		got := context.Cause(combined)
		cancelParent(nil)
		if got != first { //nolint:errorlint // abort reason identity.
			t.Fatalf("iteration %d: cause = %v, want %v", iteration, got, first)
		}
	}
}

// Review-4: a completed standard cancellation is immediately visible to a
// harness child, even while its AfterFunc notification is in flight.
func TestStandardCancellationImmediatelyVisible(t *testing.T) {
	previous := runtime.GOMAXPROCS(2)
	t.Cleanup(func() { runtime.GOMAXPROCS(previous) })
	for iteration := range 20000 {
		parent, cancelParent := context.WithCancelCause(context.Background())
		child, cancelChild := WithCancel(parent)
		reason := errors.New("custom cancellation")
		cancelParent(reason)
		if child.Err() != context.Canceled || context.Cause(child) != reason { //nolint:errorlint // exact sentinel and cause identity.
			t.Fatalf("iteration %d: err=%v cause=%v", iteration, child.Err(), context.Cause(child))
		}
		cancelChild(nil)
	}
}

// Review-4: the direct standard-signal case under concurrent delivery.
func TestStandardSignalConcurrentFirstReason(t *testing.T) {
	previous := runtime.GOMAXPROCS(2)
	t.Cleanup(func() { runtime.GOMAXPROCS(previous) })
	for iteration := range 20000 {
		signal, cancelSignal := context.WithCancelCause(context.Background())
		parent, cancelParent := WithCancel(BackgroundContext())
		combined := WithAbortSignal(parent, signal)
		first := errors.New("signal first")
		cancelSignal(first)
		cancelParent(errors.New("parent second"))
		if got := context.Cause(combined); got != first { //nolint:errorlint // abort reason identity.
			t.Fatalf("iteration %d: cause = %v, want %v", iteration, got, first)
		}
	}
}
