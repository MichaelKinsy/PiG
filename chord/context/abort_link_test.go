package chordctx

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// pi: packages/chord/src/context/abort-origin.ts
// A context derived from a source of this package is aborted inside the source's cancel function, before it returns and before
// any listener runs: Pi's `AbortSignal.any` marks every composite of a source aborted, then runs the listeners
// (abort_order_pi_oracle_test.go runs Pi's own code over the same scenarios).

// derivedAborted reports the Err of each context, in the order given.
func derivedAborted(contexts ...context.Context) []bool {
	aborted := make([]bool, len(contexts))
	for i, ctx := range contexts {
		aborted[i] = ctx.Err() != nil
	}
	return aborted
}

func TestEveryDerivedContextIsAbortedBeforeTheFirstListenerOfTheSourceRuns(t *testing.T) {
	cause := errors.New("aborted")
	source, cancel := WithCancel(Background())
	other, cancelOther := WithCancel(Background())
	defer cancelOther(nil)
	child, cancelChild := WithCancel(source)
	defer cancelChild(nil)
	composite := WithAbortSignal(other, source)
	grandchild, cancelGrandchild := WithCancel(composite)
	defer cancelGrandchild(nil)
	chained := WithAbortSignal(Background(), composite)
	stdlibChild, cancelStdlib := context.WithCancelCause(composite)
	defer cancelStdlib(nil)
	linkedChild, cancelLinked := WithAbortSignalCancel(other, source)
	defer cancelLinked(nil)
	viaLinked := WithAbortSignal(other, linkedChild)
	derived := []context.Context{child, composite, grandchild, chained, stdlibChild, linkedChild, viaLinked}

	var seen []bool
	var causes []error
	OnAbort(source, func(error) { seen = derivedAborted(derived...) })
	OnAbort(composite, func(error) { causes = append(causes, context.Cause(composite), context.Cause(viaLinked)) })
	cancel(cause)
	if len(seen) != len(derived) {
		t.Fatal("the source's first listener did not run inside its cancel")
	}
	for i, aborted := range seen {
		if !aborted {
			t.Errorf("derived context %d was still live when the source's first listener ran", i)
		}
	}
	if len(causes) != 2 || !errors.Is(causes[0], cause) || !errors.Is(causes[1], cause) {
		t.Fatalf("causes seen by the composite's listener = %v, want the source's cause", causes)
	}
	if derivedAborted(other)[0] {
		t.Fatal("the other source aborted")
	}
}

// A context derived from a signal is aborted whichever way the signal ends: its own cancel, or the cancel of an ancestor,
// where the signal itself is cancelled by the standard library's propagation (it is not the cancel function that ran).
func TestAContextDerivedFromADerivedSignalFollowsEveryCancelOfItsAncestors(t *testing.T) {
	cause := errors.New("ancestor aborted")
	t.Run("the derived signal's own cancel", func(t *testing.T) {
		root, cancelRoot := WithCancel(Background())
		defer cancelRoot(nil)
		signal, cancelSignal := WithCancel(root)
		child := WithAbortSignal(Background(), signal)
		parented := WithAbortSignal(root, signal)
		cancelSignal(cause)
		for name, ctx := range map[string]context.Context{"child": child, "parented": parented} {
			if ctx.Err() == nil || !errors.Is(context.Cause(ctx), cause) {
				t.Errorf("%s: err %v, cause %v, want aborted with the signal's cause", name, ctx.Err(), context.Cause(ctx))
			}
		}
	})
	t.Run("the root's cancel", func(t *testing.T) {
		root, cancelRoot := WithCancel(Background())
		signal, cancelSignal := WithCancel(root)
		defer cancelSignal(nil)
		child := WithAbortSignal(Background(), signal)
		nested := WithAbortSignal(Background(), child)
		other, cancelOther := WithCancel(Background())
		defer cancelOther(nil)
		parented := WithAbortSignal(other, signal)
		cancelRoot(cause)
		for name, ctx := range map[string]context.Context{"signal": signal, "child": child, "nested": nested, "parented": parented} {
			if ctx.Err() == nil || !errors.Is(context.Cause(ctx), cause) {
				t.Errorf("%s: err %v, cause %v, want aborted with the root's cause when its cancel returned", name, ctx.Err(), context.Cause(ctx))
			}
		}
	})
}

// A context linked to a signal while the signal is cancelled concurrently is aborted by the time the link returns or the
// cancel returns, whichever the later is: no link is left live.
func TestAContextLinkedWhileItsSignalIsCancelledIsNeverLeftLive(t *testing.T) {
	cause := errors.New("aborted")
	for range 20000 {
		signal, cancel := WithCancel(Background())
		parent, cancelParent := WithCancel(Background())
		linked := make(chan context.Context, 1)
		var start sync.WaitGroup
		start.Add(1)
		go func() {
			start.Wait()
			linked <- WithAbortSignal(parent, signal)
		}()
		start.Done()
		cancel(cause)
		child := <-linked
		if child.Err() == nil {
			select {
			case <-child.Done():
			case <-time.After(time.Second):
				t.Fatal("a context linked while its signal was cancelled was never cancelled")
			}
		}
		if got := context.Cause(child); !errors.Is(got, cause) {
			t.Fatalf("linked cause %v, want %v", got, cause)
		}
		cancelParent(nil)
	}
}

// The abort-mark commit of the harness cancels a run's invocation from a listener on the Session line; an operation queued
// behind that commit must already see its bound context cancelled when it runs.
func TestADerivedContextIsAbortedWhenItsSignalsCancelReturns(t *testing.T) {
	cause := errors.New("aborted")
	for range 1000 {
		signal, cancel := WithCancel(Background())
		child, release := WithAbortSignalCancel(Background(), signal)
		plain := WithAbortSignal(WithValue(Background(), NewKey[int]("k"), 1), signal)
		cancel(cause)
		if child.Err() == nil {
			t.Fatal("a linked context was still live when its signal's cancel returned")
		}
		if got := context.Cause(child); !errors.Is(got, cause) {
			t.Fatalf("linked cause %v, want %v", got, cause)
		}
		if plain.Err() == nil || !errors.Is(context.Cause(plain), cause) {
			t.Fatalf("a signal alias is %v, want aborted with the cause", plain.Err())
		}
		release(nil)
	}
}

func linksHeld(c context.Context) int {
	source := c.(*abortContext)
	source.mu.Lock()
	defer source.mu.Unlock()
	return len(source.links)
}

// A context that ended, by its own cancel, its parent's or a source's, is held by no source: finished operations do not
// accumulate on a signal that lives on.
func TestAnEndedDerivedContextIsHeldByNoSource(t *testing.T) {
	signal, cancelSignal := WithCancel(Background())
	defer cancelSignal(nil)
	ancestor, cancelAncestor := WithCancel(Background())
	defer cancelAncestor(nil)
	for range 10 {
		_, release := WithAbortSignalCancel(Background(), signal)
		release(nil)
	}
	if n := linksHeld(signal); n != 0 {
		t.Fatalf("%d released links remain registered on the signal", n)
	}
	for range 10 {
		parent, cancelParent := WithCancel(ancestor)
		WithAbortSignal(parent, signal)
		cancelParent(nil)
	}
	if n := linksHeld(signal); n != 0 {
		t.Fatalf("%d links of contexts whose parent ended remain registered on the signal", n)
	}
	if n := linksHeld(ancestor); n != 0 {
		t.Fatalf("%d links remain registered on the ancestor", n)
	}
	held, _ := WithAbortSignalCancel(ancestor, signal)
	if linksHeld(signal) != 1 || linksHeld(ancestor) != 1 {
		t.Fatalf("a live link is held by signal %d, ancestor %d, want 1 each", linksHeld(signal), linksHeld(ancestor))
	}
	cancelAncestor(nil)
	if held.Err() == nil || linksHeld(signal) != 0 {
		t.Fatalf("after its ancestor aborted: err %v, held by the signal %d", held.Err(), linksHeld(signal))
	}
}

// A signal of another package cannot be hooked: the derived context follows it on a goroutine of its own, and OnAbort says it
// is not exact; a source of this package beside it still aborts the context inside its cancel.
func TestAContextDerivedFromAForeignSignalFollowsItAndIsNotExact(t *testing.T) {
	foreign, cancelForeign := context.WithCancelCause(context.Background())
	// With a parent that has no Done channel the context is the foreign signal itself: its sources are not ours.
	alias := WithAbortSignal(WithValue(Background(), NewKey[int]("k"), 1), foreign)
	if _, exact := OnAbort(alias, func(error) {}); exact {
		t.Fatal("an alias of a foreign signal is exact")
	}
	parent, cancelParent := WithCancel(Background())
	defer cancelParent(nil)
	cause := errors.New("foreign aborted")
	under := WithAbortSignal(parent, foreign)
	if _, exact := OnAbort(under, func(error) {}); exact {
		t.Fatal("a context derived from a foreign signal is exact")
	}
	own, cancelOwn := WithCancel(Background())
	mixed := WithAbortSignal(parent, WithAbortSignal(own, foreign))
	cancelOwn(errors.New("own"))
	if mixed.Err() == nil {
		t.Fatal("the source of this package of a mixed signal did not abort the context inside its cancel")
	}
	cancelForeign(cause)
	select {
	case <-under.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("a context derived from a foreign signal never followed it")
	}
	if got := context.Cause(under); !errors.Is(got, cause) {
		t.Fatalf("cause %v, want the foreign signal's", got)
	}
}

// Listeners run in Pi's order across a source and the contexts derived from it (abort_order_pi_oracle_test.go compares Pi's
// own code): the aborted context's own first, then those of what derives from it in the order created, whenever added.
func TestOnAbortRunsAParentsListenersBeforeItsChildrenWhateverTheOrderTheyWereAdded(t *testing.T) {
	parent, cancelParent := WithCancel(Background())
	first, cancelFirst := WithCancel(parent)
	defer cancelFirst(nil)
	second, cancelSecond := WithCancel(parent)
	defer cancelSecond(nil)
	grandchild, cancelGrandchild := WithCancel(first)
	defer cancelGrandchild(nil)
	var order []string
	for _, listener := range []struct {
		name string
		ctx  context.Context
	}{{"grandchild", grandchild}, {"second", second}, {"first", first}, {"parent", parent}, {"first again", first}} {
		OnAbort(listener.ctx, func(error) { order = append(order, listener.name) })
	}
	cancelParent(nil)
	want := []string{"parent", "first", "first again", "second", "grandchild"}
	if len(order) != len(want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}
}

// A listener added by a listener to a context aborted already runs at once, as Pi's callers check `signal.aborted` before they
// add one; a context cancelled by its own cancel while a listener of the same abort registers is either in the snapshot or runs
// at once, and never twice.
func TestOnAbortFromAListenerOfTheSameAbortRunsAtOnceOnce(t *testing.T) {
	parent, cancelParent := WithCancel(Background())
	child, cancelChild := WithCancel(parent)
	defer cancelChild(nil)
	var order []string
	OnAbort(parent, func(error) {
		order = append(order, "parent")
		OnAbort(child, func(error) { order = append(order, "late child") })
		order = append(order, "after")
	})
	OnAbort(child, func(error) { order = append(order, "child") })
	cancelParent(nil)
	want := []string{"parent", "late child", "after", "child"}
	if len(order) != len(want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}
}
