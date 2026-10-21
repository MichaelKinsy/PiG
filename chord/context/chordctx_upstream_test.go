package chordctx

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/telemetry"
)

// Ports packages/chord/test/context.test.ts. The context is a Go context; its toString is its String method, and Background and TODO are Pi's roots.

func TestContextProvidesDistinctEmptyRootContexts(t *testing.T) {
	key := NewKey[string]("value")
	if TODO() == Background() {
		t.Fatal("TODO and Background are the same context")
	}
	if TODO().Err() != nil || TODO().Done() != nil {
		t.Fatal("TODO carries cancellation")
	}
	if Telemetry(TODO()) != telemetry.NoopTelemetryContext || Telemetry(Background()) != telemetry.NoopTelemetryContext {
		t.Fatal("a root context carries telemetry other than the no-op parent")
	}
	if _, ok := Value(TODO(), key); ok {
		t.Fatal("TODO carries a value")
	}
	if got := fmt.Sprint(Background()); got != "[Context BACKGROUND_CONTEXT]" {
		t.Fatalf("Background string = %q", got)
	}
	if got := fmt.Sprint(TODO()); got != "[Context TODO_CONTEXT]" {
		t.Fatalf("TODO string = %q", got)
	}
}

func TestContextLayersTypedValuesWithoutModifyingParents(t *testing.T) {
	first, second := NewKey[string]("first"), NewKey[int]("second")
	firstContext := WithValue(Background(), first, "one")
	secondContext := WithValue(firstContext, second, 2)
	replaced := WithValue(secondContext, first, "updated")

	expect := func(ctx context.Context, key Key[string], want string, present bool) {
		t.Helper()
		got, ok := Value(ctx, key)
		if ok != present || got != want {
			t.Fatalf("Value=%q,%v want %q,%v", got, ok, want, present)
		}
	}
	expectInt := func(ctx context.Context, want int, present bool) {
		t.Helper()
		got, ok := Value(ctx, second)
		if ok != present || got != want {
			t.Fatalf("Value=%d,%v want %d,%v", got, ok, want, present)
		}
	}
	expect(Background(), first, "", false)
	expect(firstContext, first, "one", true)
	expectInt(firstContext, 0, false)
	expect(secondContext, first, "one", true)
	expectInt(secondContext, 2, true)
	expect(replaced, first, "updated", true)
	expect(secondContext, first, "one", true)
	if got := fmt.Sprint(replaced); got != "[Context BACKGROUND_CONTEXT].WithValue(first).WithValue(second).WithValue(first)" {
		t.Fatalf("String = %q", got)
	}

	// Two keys with one description and one type are still distinct.
	twin := NewKey[string]("first")
	expect(firstContext, twin, "", false)
}

// context/index.ts withAbortSignal, withoutAbortSignal and withCancel derive a ContextValue of the abort signal key, so they print as one.
func TestContextDerivedAbortContextsPrintAsPiPrintsThem(t *testing.T) {
	signal, abort := context.WithCancelCause(Background())
	defer abort(nil)
	withSignal := WithAbortSignal(TODO(), signal)
	cancelable, cancel := WithCancel(withSignal)
	defer cancel(nil)
	for _, c := range []struct {
		name string
		ctx  context.Context
		want string
	}{
		{"withAbortSignal", withSignal, "[Context TODO_CONTEXT].WithValue(chord.abortSignal)"},
		{"withoutAbortSignal", WithoutAbortSignal(withSignal), "[Context TODO_CONTEXT].WithValue(chord.abortSignal).WithValue(chord.abortSignal)"},
		{"withCancel", cancelable, "[Context TODO_CONTEXT].WithValue(chord.abortSignal).WithValue(chord.abortSignal)"},
		{"a value over withCancel", WithValue(cancelable, NewKey[string]("k"), "v"), "[Context TODO_CONTEXT].WithValue(chord.abortSignal).WithValue(chord.abortSignal).WithValue(k)"},
	} {
		if got := fmt.Sprint(c.ctx); got != c.want {
			t.Errorf("%s string = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestContextInheritsParentCancellationAndIsolatesChildCancellation(t *testing.T) {
	parentSource, cancelParent := context.WithCancelCause(context.Background())
	parent := WithAbortSignal(context.Background(), parentSource)
	child, cancelChild := WithCancel(parent)
	sibling, _ := WithCancel(parent)

	cancelChild(errors.New("child"))
	if child.Err() == nil || context.Cause(child).Error() != "child" {
		t.Fatalf("child err=%v cause=%v", child.Err(), context.Cause(child))
	}
	if sibling.Err() != nil || parent.Err() != nil {
		t.Fatal("child cancellation reached its sibling or parent")
	}
	select {
	case <-child.Done():
	default:
		t.Fatal("child Done is not closed")
	}

	cancelParent(errors.New("parent"))
	<-sibling.Done()
	if context.Cause(sibling).Error() != "parent" {
		t.Fatalf("sibling cause=%v", context.Cause(sibling))
	}
}

func TestContextMasksCallerCancellationForMandatoryCleanup(t *testing.T) {
	signal, abort := context.WithCancelCause(context.Background())
	key := NewKey[string]("value")
	ctx := WithValue(WithAbortSignal(context.Background(), signal), key, "preserved")
	cleanup := WithoutAbortSignal(ctx)

	abort(nil)
	<-ctx.Done()
	if ctx.Err() == nil {
		t.Fatal("context was not cancelled")
	}
	if cleanup.Err() != nil || cleanup.Done() != nil {
		t.Fatal("cleanup context observes cancellation")
	}
	if got, _ := Value(cleanup, key); got != "preserved" {
		t.Fatalf("value=%q", got)
	}
}

func TestContextStopsWaitingWhenTheInvocationIsCancelled(t *testing.T) {
	cancellation := errors.New("cancelled")
	// The abort comes while Await is already waiting (synctest.Wait), so the rejection comes from the wait itself, as the abort listener's does,
	// not from Await's check of an already cancelled context.
	synctest.Test(t, func(t *testing.T) {
		signal, abort := context.WithCancelCause(context.Background())
		ctx := WithAbortSignal(context.Background(), signal)
		work := make(chan Settled[string], 1)
		waiting := make(chan error, 1)
		go func() { _, err := Await(ctx, work); waiting <- err }()
		synctest.Wait()

		abort(cancellation)
		if err := <-waiting; err != cancellation {
			t.Fatalf("Await error = %v, want the cancellation cause", err)
		}
		work <- Settled[string]{Value: "completed later"}
		if later := <-work; later.Value != "completed later" {
			t.Fatal("cancelled wait consumed the work")
		}
	})

	done := make(chan Settled[string], 1)
	done <- Settled[string]{Value: "completed"}
	if got, err := Await(context.Background(), done); err != nil || got != "completed" {
		t.Fatalf("Await = %q, %v", got, err)
	}
	failed := make(chan Settled[string], 1)
	failure := errors.New("work failed")
	failed <- Settled[string]{Err: failure}
	if _, err := Await(context.Background(), failed); err != failure {
		t.Fatalf("Await error = %v", err)
	}
	already, stop := context.WithCancelCause(context.Background())
	stop(cancellation)
	if _, err := Await(already, make(chan Settled[string])); err != cancellation {
		t.Fatalf("pre-cancelled Await error = %v", err)
	}
}

// context/index.ts withAbortSignal combines signals with AbortSignal.any, which returns an already aborted signal when an input is aborted, taking the first aborted input's reason in [parent, signal] order. awaitWithContext then rejects without waiting for the work.
func TestContextWithAnAlreadyCancelledSignalIsCancelledOnReturn(t *testing.T) {
	cancellation := errors.New("cancelled")
	signal, abort := context.WithCancelCause(context.Background())
	abort(cancellation)
	for range 1000 {
		ctx := WithAbortSignal(context.Background(), signal)
		if ctx.Err() == nil || context.Cause(ctx) != cancellation {
			t.Fatalf("err=%v cause=%v, want the signal's cause on return", ctx.Err(), context.Cause(ctx))
		}
		ready := make(chan Settled[string], 1)
		ready <- Settled[string]{Value: "completed"}
		if _, err := Await(ctx, ready); err != cancellation {
			t.Fatalf("Await error = %v, want the cancellation cause", err)
		}
	}
	parentCause := errors.New("parent")
	parent, abortParent := context.WithCancelCause(context.Background())
	abortParent(parentCause)
	if got := context.Cause(WithAbortSignal(parent, signal)); got != parentCause {
		t.Fatalf("cause = %v, want the parent's", got)
	}
}

// Context.toString (context/index.ts:9, 28, 50) is the String method of each layer: a root prints its name, a value layer prints `<parent>.WithValue(<key description>)`, and the abort-signal layers print the abort key's description. Calling String directly pins what String(context) in context.test.ts reads.
func TestContextToStringIsTheStringMethodOfEveryLayer(t *testing.T) {
	stringOf := func(ctx context.Context) string {
		stringer, ok := ctx.(fmt.Stringer)
		if !ok {
			t.Fatalf("%T has no String method", ctx)
		}
		return stringer.String()
	}
	if got := stringOf(Background()); got != "[Context BACKGROUND_CONTEXT]" {
		t.Fatalf("Background String = %q", got)
	}
	if got := stringOf(TODO()); got != "[Context TODO_CONTEXT]" {
		t.Fatalf("TODO String = %q", got)
	}
	layered := WithValue(WithValue(Background(), NewKey[string]("first"), "one"), NewKey[int]("second"), 2)
	if got := stringOf(layered); got != "[Context BACKGROUND_CONTEXT].WithValue(first).WithValue(second)" {
		t.Fatalf("value layers String = %q", got)
	}
	if got := stringOf(WithoutAbortSignal(TODO())); got != "[Context TODO_CONTEXT].WithValue(chord.abortSignal)" {
		t.Fatalf("abort layer String = %q", got)
	}
}

// context.test.ts "derives telemetry independently from values and cancellation". The Go callback of TraceIn receives the derived context and reads the span as Telemetry(child), where Pi reads `child.telemetry`.
func TestContextDerivesTelemetryIndependentlyFromValuesAndCancellation(t *testing.T) {
	recorder := telemetry.NewInMemoryTelemetryContext()
	other := telemetry.NewInMemoryTelemetryContext()
	if err := recorder.StartSpan(telemetry.SpanOptions{Name: "caller"}, func(callerSpan telemetry.TelemetrySpan) error {
		return other.StartSpan(telemetry.SpanOptions{Name: "other"}, func(otherSpan telemetry.TelemetrySpan) error {
			testContextDerivesTelemetry(t, recorder, callerSpan, otherSpan)
			return nil
		})
	}); err != nil {
		t.Fatal(err)
	}
}

func testContextDerivesTelemetry(t *testing.T, recorder *telemetry.InMemoryTelemetryContext, callerSpan, otherSpan telemetry.TelemetrySpan) {
	t.Helper()
	key := NewKey[string]("principal")
	signal, abort := context.WithCancelCause(context.Background())
	defer abort(nil)
	base := WithValue(WithAbortSignal(Background(), signal), key, "owner")
	derived := WithTelemetry(base, callerSpan)
	replaced := WithTelemetry(derived, otherSpan)
	if Telemetry(base) != telemetry.NoopTelemetryContext {
		t.Fatal("deriving telemetry changed the parent")
	}
	if Telemetry(derived) != callerSpan || Telemetry(replaced) != otherSpan {
		t.Fatal("Telemetry did not return the derived telemetry")
	}
	if got, _ := Value(replaced, key); got != "owner" {
		t.Fatalf("value = %q", got)
	}
	if replaced.Done() != base.Done() {
		t.Fatal("deriving telemetry changed cancellation")
	}
	if Telemetry(WithoutAbortSignal(derived)) != callerSpan {
		t.Fatal("WithoutAbortSignal dropped the telemetry")
	}
	if got := fmt.Sprint(derived); got != "[Context BACKGROUND_CONTEXT].WithValue(chord.abortSignal).WithValue(principal).WithValue(chord.telemetry)" {
		t.Fatalf("String = %q", got)
	}

	_, err := TraceIn(derived, telemetry.SpanOptions{Name: "outer"}, func(child context.Context) (struct{}, error) {
		outer := Telemetry(child)
		if got, _ := Value(child, key); got != "owner" {
			t.Fatalf("child value = %q", got)
		}
		if child.Done() != derived.Done() {
			t.Fatal("the child span's context changed cancellation")
		}
		if Telemetry(child) == callerSpan {
			t.Fatal("the derived context's telemetry is the parent, not the new span")
		}
		outer.AddEvent("started", nil)
		return TraceIn(child, telemetry.SpanOptions{Name: "inner"}, func(innerContext context.Context) (struct{}, error) {
			inner := Telemetry(innerContext)
			inner.AddLink(telemetry.SpanLink{Context: outer})
			return struct{}{}, nil
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	spans := recorder.GetSpans()
	if len(spans) != 3 {
		t.Fatalf("spans = %d, want 3", len(spans))
	}
	caller, outer, inner := spans[0], spans[1], spans[2]
	if outer.ParentID == nil || *outer.ParentID != caller.ID {
		t.Fatalf("outer parent = %v, want %d", outer.ParentID, caller.ID)
	}
	if inner.ParentID == nil || *inner.ParentID != outer.ID {
		t.Fatalf("inner parent = %v, want %d", inner.ParentID, outer.ID)
	}
	if len(inner.Links) != 1 || !reflect.DeepEqual(inner.Links[0].Context, outer.Context) {
		t.Fatalf("inner links = %+v, want the outer context %v", inner.Links, outer.Context)
	}
	if Telemetry(derived) != callerSpan {
		t.Fatal("TraceIn changed the context it was given")
	}
	abort(errors.New("aborted"))
	<-derived.Done()
	if derived.Err() == nil {
		t.Fatal("derived context did not observe cancellation")
	}
}

// context.test.ts "isolates concurrent traceIn branches and preserves callback results and errors".
func TestContextIsolatesConcurrentTraceInBranchesAndPreservesCallbackResultsAndErrors(t *testing.T) {
	recorder := telemetry.NewInMemoryTelemetryContext()
	if err := recorder.StartSpan(telemetry.SpanOptions{Name: "caller"}, func(parent telemetry.TelemetrySpan) error {
		testIsolatesConcurrentTraceInBranches(t, recorder, parent)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func testIsolatesConcurrentTraceInBranches(t *testing.T, recorder *telemetry.InMemoryTelemetryContext, parentSpan telemetry.TelemetrySpan) {
	t.Helper()
	ctx := WithTelemetry(Background(), parentSpan)
	gate := make(chan struct{})
	first := make(chan Settled[string], 1)
	second := make(chan Settled[string], 1)
	go func() {
		value, err := TraceIn(ctx, telemetry.SpanOptions{Name: "first"}, func(child context.Context) (string, error) {
			<-gate
			return TraceIn(child, telemetry.SpanOptions{Name: "first.child"}, func(context.Context) (string, error) { return "first", nil })
		})
		first <- Settled[string]{Value: value, Err: err}
	}()
	go func() {
		value, err := TraceIn(ctx, telemetry.SpanOptions{Name: "second"}, func(child context.Context) (string, error) {
			result, err := TraceIn(child, telemetry.SpanOptions{Name: "second.child"}, func(context.Context) (string, error) { return "second", nil })
			close(gate)
			return result, err
		})
		second <- Settled[string]{Value: value, Err: err}
	}()
	for name, work := range map[string]chan Settled[string]{"first": first, "second": second} {
		outcome := <-work
		if outcome.Err != nil || outcome.Value != name {
			t.Fatalf("%s = %q, %v", name, outcome.Value, outcome.Err)
		}
	}
	spans := recorder.GetSpans()
	find := func(name string) telemetry.RecordedTelemetrySpan {
		t.Helper()
		for _, span := range spans {
			if span.Name == name {
				return span
			}
		}
		t.Fatalf("no span %q in %d spans", name, len(spans))
		return telemetry.RecordedTelemetrySpan{}
	}
	for _, name := range []string{"first", "second"} {
		parent, child := find(name), find(name+".child")
		if child.ParentID == nil || *child.ParentID != parent.ID {
			t.Fatalf("%s parent = %v, want %d", child.Name, child.ParentID, parent.ID)
		}
	}

	failure := errors.New("same value")
	if _, err := TraceIn(ctx, telemetry.SpanOptions{Name: "reject"}, func(context.Context) (struct{}, error) { return struct{}{}, failure }); err != failure {
		t.Fatalf("error = %v, want the callback's own", err)
	}
	// Upstream's synchronous throw is a panic in Go: it reaches the caller as the identical value, after the span ends.
	func() {
		defer func() {
			if recovered := recover(); recovered != any(failure) {
				t.Fatalf("recovered = %v, want the callback's own panic value", recovered)
			}
		}()
		_, _ = TraceIn(ctx, telemetry.SpanOptions{Name: "throw"}, func(context.Context) (struct{}, error) { panic(failure) })
		t.Fatal("TraceIn returned after a panicking callback")
	}()
	value, err := TraceIn(Background(), telemetry.SpanOptions{Name: "noop"}, func(child context.Context) (int, error) {
		span := Telemetry(child)
		span.AddLink(telemetry.SpanLink{Context: parentSpan})
		return 42, nil
	})
	if err != nil || value != 42 {
		t.Fatalf("no-op trace = %d, %v", value, err)
	}
}
