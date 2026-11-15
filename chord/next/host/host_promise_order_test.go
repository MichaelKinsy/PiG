package host

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"weak"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
)

// Own tests of thenWithContext against Pi's `awaitWithContext(promise, context)` (packages/chord/src/context/index.ts:138-153).
// Pi adds an abort listener that `abort()` runs synchronously, before it returns, and that rejects the outer promise at
// once; the promise's own settlement reaches the outer promise through a reaction, a microtask that runs after the section
// that settled it. So the abort wins when it comes before that reaction runs, and the result wins when the reaction ran
// before the abort: the order of the two events, never a race of two threads. An external abort signal fires in a task of
// its own, after the section (and its microtasks) that it did not interrupt.

var errOrderAbort = errors.New("aborted")

// orderWait is one thenWithContext waiter and what its callback got, in order. Its fields belong to the sections of loop.
type orderWait struct {
	h       *memberHarness
	promise *keyedPromise[int]
	got     []any
}

// newOrderWait registers a waiter on a fresh promise and a fresh cancellable context.
func newOrderWait(h *memberHarness) (*orderWait, func(cause error)) {
	ctx, cancel := chordctx.WithCancel(memberBackground)
	w := &orderWait{h: h, promise: newKeyedPromise[int](h.loop)}
	h.loop.Run(func() {
		w.promise.thenWithContext(ctx, func(value int, err error) { w.got = append(w.got, value, err) })
	})
	return w, cancel
}

// outcome is what the callback got, read once every task the loop owns has returned.
func (w *orderWait) outcome() []any {
	w.h.loop.Wait()
	var got []any
	w.h.loop.Run(func() { got = append(got, w.got...) })
	return got
}

var (
	orderAborted = []any{0, errOrderAbort}
	orderValue   = []any{1, nil}
)

// orderRuns is how many times each order is tried: the failure of the old code was a race that lost a few in a hundred.
const orderRuns = 100

func eachOrder(t *testing.T, name string, order func(t *testing.T, h *memberHarness, w *orderWait, cancel func(error)), want []any) {
	t.Run(name, func(t *testing.T) {
		h := newMemberHarness(t)
		wrong := 0
		for range orderRuns {
			w, cancel := newOrderWait(h)
			order(t, h, w, cancel)
			if got := w.outcome(); !equalAny(got, want) {
				wrong++
			}
		}
		if wrong != 0 {
			t.Fatalf("%d of %d runs delivered the wrong outcome; want %v", wrong, orderRuns, want)
		}
	})
}

func equalAny(a, b []any) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestThenWithContextOrdersAbortAgainstSettlement(t *testing.T) {
	// Pi: the abort listener runs inside abort(); the waiter's continuation is queued there. The abort of a section is
	// delivered before a microtask the section queues after it, with no thread in between.
	t.Run("an abort inside a section is delivered before the microtask queued after it", func(t *testing.T) {
		h := newMemberHarness(t)
		wrong := 0
		for range orderRuns {
			w, cancel := newOrderWait(h)
			var seen []any
			h.loop.Run(func() {
				cancel(errOrderAbort)
				h.loop.Queue(func() { seen = append(seen, w.got...) })
			})
			if !equalAny(seen, orderAborted) {
				wrong++
			}
			w.outcome()
		}
		if wrong != 0 {
			t.Fatalf("%d of %d runs: the continuation of the abort had not run when the next microtask did", wrong, orderRuns)
		}
	})
	eachOrder(t, "the abort first, in one section with the settlement", func(t *testing.T, h *memberHarness, w *orderWait, cancel func(error)) {
		h.loop.Run(func() {
			cancel(errOrderAbort)
			w.promise.resolve(1)
		})
	}, orderAborted)
	// Pi: reject() runs inside abort(); the reaction of the settlement is still a microtask that did not run, so resolve() in
	// it finds the outer promise rejected.
	eachOrder(t, "the settlement first, in one section with the abort: its reaction has not run", func(t *testing.T, h *memberHarness, w *orderWait, cancel func(error)) {
		h.loop.Run(func() {
			w.promise.resolve(1)
			cancel(errOrderAbort)
		})
	}, orderAborted)
	// The settlement's section ended, so its reaction ran: the abort of the next section finds the outer promise resolved.
	eachOrder(t, "the settlement first, in a section before the abort's", func(t *testing.T, h *memberHarness, w *orderWait, cancel func(error)) {
		h.loop.Run(func() { w.promise.resolve(1) })
		h.loop.Run(func() { cancel(errOrderAbort) })
	}, orderValue)
	// The same, the abort given by a goroutine that is no section of the loop.
	eachOrder(t, "the settlement first, in a section before an abort from outside", func(t *testing.T, h *memberHarness, w *orderWait, cancel func(error)) {
		h.loop.Run(func() { w.promise.resolve(1) })
		cancel(errOrderAbort)
	}, orderValue)
	// The production fault: the abort of one section, the settlement of the next, back to back.
	eachOrder(t, "the abort in one section, the settlement in the next", func(t *testing.T, h *memberHarness, w *orderWait, cancel func(error)) {
		h.loop.Run(func() { cancel(errOrderAbort) })
		h.loop.Run(func() { w.promise.resolve(1) })
	}, orderAborted)
	// An abort from outside is observed at the time it is made: a settlement after it comes after it in the loop's queue.
	eachOrder(t, "an abort from outside, then a settlement", func(t *testing.T, h *memberHarness, w *orderWait, cancel func(error)) {
		cancel(errOrderAbort)
		h.loop.Run(func() { w.promise.resolve(1) })
	}, orderAborted)
	// An external abort signal fires in a task of its own: a settlement whose reaction is queued ahead of it, in a section
	// that was running, is delivered first.
	eachOrder(t, "an abort from outside while a settlement's reaction is queued", func(t *testing.T, h *memberHarness, w *orderWait, cancel func(error)) {
		h.loop.Run(func() {
			w.promise.resolve(1)
			done := make(chan struct{})
			go func() {
				defer close(done)
				cancel(errOrderAbort)
			}()
			<-done
		})
	}, orderValue)
	// A source OnAbort cannot listen to (a context of another package) is observed by context.AfterFunc: the abort from
	// outside is delivered, as a task of its own.
	t.Run("an abort of a context of another package is delivered", func(t *testing.T) {
		h := newMemberHarness(t)
		wrong := 0
		for range orderRuns {
			ctx, cancel := context.WithCancelCause(memberBackground)
			promise := newKeyedPromise[int](h.loop)
			delivered := make(chan []any, 1)
			h.loop.Run(func() {
				promise.thenWithContext(ctx, func(value int, err error) { delivered <- []any{value, err} })
			})
			cancel(errOrderAbort)
			if !equalAny(<-delivered, orderAborted) {
				wrong++
			}
		}
		if wrong != 0 {
			t.Fatalf("%d of %d runs delivered the wrong outcome; want %v", wrong, orderRuns, orderAborted)
		}
	})
}

// A wait that its promise settled first keeps no listener on its context (Pi removes it in the reaction): 10,000 of them
// leave nothing of theirs reachable from the context.
func TestThenWithContextLeavesNoListenerBehindWhenTheResultComesFirst(t *testing.T) {
	h := newMemberHarness(t)
	ctx, cancel := chordctx.WithCancel(memberBackground)
	defer cancel(nil)
	const waits = 10000
	promises := make([]weak.Pointer[keyedPromise[int]], 0, waits)
	h.loop.Run(func() {
		for range waits {
			promise := newKeyedPromise[int](h.loop)
			promise.thenWithContext(ctx, func(int, error) {})
			promise.resolve(1)
			promises = append(promises, weak.Make(promise))
		}
	})
	runtime.GC()
	kept := 0
	for _, promise := range promises {
		if promise.Value() != nil {
			kept++
		}
	}
	if kept != 0 {
		t.Fatalf("%d of %d promises are still held by the context's listeners", kept, waits)
	}
}
