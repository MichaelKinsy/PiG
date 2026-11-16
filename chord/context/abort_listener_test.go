package chordctx

import (
	"context"
	"errors"
	"testing"
)

// OnAbort is the Go form of `signal.addEventListener("abort", listener)`: the listener runs inside the cancel function that
// aborts the context, before it returns (context/index.ts:138-153 `awaitWithContext`).

var errListened = errors.New("listened")

func TestOnAbortRunsInsideTheCancelFunctionOnce(t *testing.T) {
	ctx, cancel := WithCancel(Background())
	var calls []error
	stop, exact := OnAbort(ctx, func(cause error) { calls = append(calls, cause) })
	if !exact {
		t.Fatal("a context of this package alone is not exact")
	}
	if len(calls) != 0 {
		t.Fatalf("ran before the abort: %v", calls)
	}
	cancel(errListened)
	if len(calls) != 1 || !errors.Is(calls[0], errListened) {
		t.Fatalf("calls after cancel returned = %v, want [%v]", calls, errListened)
	}
	cancel(errors.New("again"))
	if len(calls) != 1 {
		t.Fatalf("ran twice: %v", calls)
	}
	if stop() {
		t.Fatal("stop reports it kept fn from running after fn ran")
	}
}

func TestOnAbortOnAnAbortedContextRunsAtOnce(t *testing.T) {
	ctx, cancel := WithCancel(Background())
	cancel(errListened)
	var calls []error
	OnAbort(ctx, func(cause error) { calls = append(calls, cause) })
	if len(calls) != 1 || !errors.Is(calls[0], errListened) {
		t.Fatalf("calls before OnAbort returned = %v, want [%v]", calls, errListened)
	}
	// Under a parent aborted before, and through a value wrapper.
	parent, cancelParent := WithCancel(Background())
	cancelParent(errListened)
	child, _ := WithCancel(parent)
	calls = nil
	OnAbort(WithValue(child, NewKey[int]("k"), 1), func(cause error) { calls = append(calls, cause) })
	if len(calls) != 1 || !errors.Is(calls[0], errListened) {
		t.Fatalf("child of an aborted parent: calls = %v", calls)
	}
}

func TestOnAbortRunsListenersInTheOrderTheyWereAdded(t *testing.T) {
	ctx, cancel := WithCancel(Background())
	var order []int
	for i := range 5 {
		OnAbort(ctx, func(error) { order = append(order, i) })
	}
	cancel(nil)
	if len(order) != 5 || order[0] != 0 || order[1] != 1 || order[2] != 2 || order[3] != 3 || order[4] != 4 {
		t.Fatalf("order = %v", order)
	}
}

func TestOnAbortStopKeepsTheListenerFromRunning(t *testing.T) {
	ctx, cancel := WithCancel(Background())
	ran := false
	stop, _ := OnAbort(ctx, func(error) { ran = true })
	if !stop() {
		t.Fatal("stop of a listener that did not run reports false")
	}
	if stop() {
		t.Fatal("a second stop reports true")
	}
	cancel(nil)
	if ran {
		t.Fatal("a stopped listener ran")
	}
}

// A listener is held by every source it listens to until it is stopped (a cancel clears its own source's set): no
// registration outlives its wait. 10,000 waits that stopped leave the sets empty.
func TestOnAbortLeavesNoRegistrationBehind(t *testing.T) {
	parent, cancelParent := WithCancel(Background())
	signal, cancelSignal := WithCancel(Background())
	child, cancelChild := WithCancel(parent)
	derived := WithAbortSignal(child, signal)
	defer cancelParent(nil)
	defer cancelSignal(nil)
	defer cancelChild(nil)
	sources := map[string]*abortContext{"parent": parent.(*abortContext), "child": child.(*abortContext), "signal": signal.(*abortContext)}
	held := func(name string) int {
		sources[name].mu.Lock()
		defer sources[name].mu.Unlock()
		return len(sources[name].listeners)
	}
	for range 10000 {
		stop, _ := OnAbort(derived, func(error) {})
		stop()
	}
	for name := range sources {
		if n := held(name); n != 0 {
			t.Fatalf("%s holds %d listeners after every wait stopped", name, n)
		}
	}
	stop, _ := OnAbort(derived, func(error) {})
	if held("parent") != 1 || held("child") != 1 || held("signal") != 1 {
		t.Fatalf("a listener is not held by each of its sources: parent %d child %d signal %d", held("parent"), held("child"), held("signal"))
	}
	cancelParent(nil)
	if n := held("parent"); n != 0 {
		t.Fatalf("the cancel left %d listeners in its own set", n)
	}
	stop()
	for name := range sources {
		if n := held(name); n != 0 {
			t.Fatalf("%s holds %d listeners after the wait stopped", name, n)
		}
	}
}

// A context derived from sources runs the listener inside the cancel of the first source to abort, with that source's cause,
// before the derived context's own Done follows (WithAbortSignal and a child of a cancelled parent propagate later).
func TestOnAbortListensToEverySourceOfADerivedContext(t *testing.T) {
	first, second := errors.New("first"), errors.New("second")
	parent, cancelParent := WithCancel(Background())
	signal, cancelSignal := WithCancel(Background())
	derived := WithAbortSignal(parent, signal)
	var causes []error
	OnAbort(derived, func(cause error) { causes = append(causes, cause) })
	cancelSignal(first)
	if len(causes) != 1 || !errors.Is(causes[0], first) {
		t.Fatalf("causes after the signal's cancel = %v", causes)
	}
	cancelParent(second)
	if len(causes) != 1 {
		t.Fatalf("ran again for the second source: %v", causes)
	}

	grandparent, cancelGrandparent := WithCancel(Background())
	child, _ := WithCancel(grandparent)
	causes = nil
	OnAbort(WithValue(child, NewKey[int]("k"), 1), func(cause error) { causes = append(causes, cause) })
	cancelGrandparent(first)
	if len(causes) != 1 || !errors.Is(causes[0], first) {
		t.Fatalf("causes after the grandparent's cancel = %v", causes)
	}
}

func TestOnAbortSaysWhatItCannotListenTo(t *testing.T) {
	stdlib := t.Context()
	under, cancelUnder := WithCancel(stdlib)
	defer cancelUnder(nil)
	cases := map[string]struct {
		ctx   context.Context
		exact bool
	}{
		"background":            {Background(), true},
		"detached":              {WithoutAbortSignal(Background()), true},
		"stdlib cancel":         {stdlib, false},
		"under a stdlib cancel": {under, false},
	}
	for name, c := range cases {
		if _, exact := OnAbort(c.ctx, func(error) {}); exact != c.exact {
			t.Errorf("%s: exact = %v, want %v", name, exact, c.exact)
		}
	}
}
