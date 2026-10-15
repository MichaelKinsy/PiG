package chordctx

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"testing/synctest"
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
