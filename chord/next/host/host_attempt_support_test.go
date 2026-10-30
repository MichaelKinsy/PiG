package host

import (
	"context"
	"runtime"
	"testing"
	"time"
	"weak"

	"github.com/MichaelKinsy/PiG/chord"
	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

// Helpers of the attempt, object, routines, retry-on and ref-state tests: what Pi's tests do with `await` on a reference
// and its state, with the Go forms of its handlers (blocking functions off the loop, reading the host's state through a
// section). Time that Pi's fake timers drive is the scopeLoop of host_scope_support_test.go.

// hangGuard is how long a test waits for something that must happen before it calls the host hung. It bounds a failure; no
// test waits for it to pass.
const hangGuard = 30 * time.Second

// enterOf is a section of the host of self's object: what code outside the host's loop (a handler, an opener) enters to read
// or change the host's state (Pi's handlers run on its event loop).
func enterOf(self Self) func(work func()) { return self.(*attempt).object.host.enter }

// mustGet is `await host.get(Type, params, context)`.
func mustGet(t testing.TB, host Host, typ *contracts.Contract, params *delta.JsonObject) *Reference {
	t.Helper()
	reference, err := host.Get(typ, params, background, GetOptions{})
	must(t, err)
	return reference
}

// mustHold is `host.hold(Type, params, context)`.
func mustHold(t testing.TB, host Host, typ *contracts.Contract, params *delta.JsonObject) *Reference {
	t.Helper()
	reference, err := host.Hold(typ, params, background, GetOptions{})
	must(t, err)
	return reference
}

// stateOfRef is `ref.state.value`, read in a section.
func stateOfRef(ref *Reference) *RefState {
	var current *RefState
	ref.binding.loop.Run(func() { current = ref.State().Value() })
	return current
}

// untilState waits until the state of ref satisfies ok and returns it: it watches `ref.state`, so every state in between
// is seen, as a test that awaits the state does.
func untilState(t testing.TB, ref *Reference, ok func(*RefState) bool) *RefState {
	t.Helper()
	watch, err := refStateWatch(ref, background)
	must(t, err)
	defer func() { _, _ = watch.Stop() }()
	if current := watch.Value(); ok(current) {
		return current
	}
	states := make(chan *RefState, 256)
	watch.Start(func(_ context.Context, call chord.NativeWatchCall[*RefState]) error {
		if call.Kind == chord.WatchCallValue {
			states <- call.Value
		}
		return nil
	})
	guard := time.NewTimer(hangGuard)
	defer guard.Stop()
	for {
		select {
		case next := <-states:
			if ok(next) {
				return next
			}
		case <-guard.C:
			t.Fatalf("the reference never reached the awaited state; it is %s", stateOfRef(ref).Kind)
			return nil
		}
	}
}

// untilKind is untilState for a kind of state.
func untilKind(t testing.TB, ref *Reference, kind RefStateKind) *RefState {
	t.Helper()
	return untilState(t, ref, func(s *RefState) bool { return s.Kind == kind })
}

// receive waits for the next value of ch (a handler's signal) and fails the test when none comes (an error, not Fatal: it
// may be called from a handler's task).
func receive[T any](t testing.TB, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(hangGuard):
		t.Error("the awaited signal never came")
		var zero T
		return zero
	}
}

// signal is a one-shot signal: Pi's `Promise.withResolvers<void>()` that only resolves.
type signal chan struct{}

func newSignal() signal { return make(signal) }

// fire resolves the signal once.
func (s signal) fire() {
	select {
	case <-s:
	default:
		close(s)
	}
}

// wait blocks until the signal fired.
func (s signal) wait(t testing.TB) {
	t.Helper()
	receive(t, (<-chan struct{})(s))
}

// await is `await promise` in a handler: the handler suspends (its call's section goes on) and waits until the signal fired,
// whatever its context does.
func (s signal) await(t testing.TB, ctx context.Context) {
	t.Helper()
	chordctx.Suspend(ctx)
	s.wait(t)
}

// waitMain blocks the test's own goroutine until the signal fired.
func (s signal) waitMain(t testing.TB) {
	t.Helper()
	s.wait(t)
}

// refCall is `await ref.method(args, context)`.
func refCall(ref *Reference, method string, args delta.JsonValue) (delta.JsonValue, error) {
	return ref.Call(method, args, background)
}

// refStateWatch is `await ref.state.watch(ctx)`: the watch is taken in a section, as the reference's state is read there.
func refStateWatch(ref *Reference, ctx context.Context) (chord.NativeWatchHandle[*RefState], error) {
	var watch chord.NativeWatchHandle[*RefState]
	var err error
	ref.binding.loop.Run(func() { watch, err = ref.State().Watch(ctx) })
	return watch, err
}

// untilEnded waits for every teardown the host still runs (an ending object's drain, closes and release of its references).
func untilEnded(t testing.TB, host Host) {
	t.Helper()
	h := host.(*chordHost)
	until(t, "the host's endings", func() bool {
		pending := 0
		h.enter(func() { pending = len(h.endings) })
		return pending == 0
	})
}

// hasTimerAt is whether a timer of the fake clock is due at ms.
func hasTimerAt(loop *scopeLoop, ms int64) bool {
	loop.mu.Lock()
	defer loop.mu.Unlock()
	for _, timer := range loop.timers {
		if timer.at == ms {
			return true
		}
	}
	return false
}

// advanceSettled is advanceHost with the host's tasks settled before the next timer: the next backoff or deadline timer
// exists only once the attempt that failed has reported (a task), as it does after the microtasks Pi's
// `advanceTimersByTimeAsync` awaits. A task a test holds blocked on purpose is waited for 200 ms at most.
func advanceSettled(loop *scopeLoop, host Host, ms int64) {
	settle := func() {
		// A task a test holds on purpose (a gate) is not waited for past a bound.
		for waited := 0; loop.awaiting.Load() != 0 && waited < 200; waited++ {
			time.Sleep(time.Millisecond)
		}
		turn(host)
	}
	settle()
	end := loop.Now() + ms
	for {
		next, found := int64(0), false
		loop.mu.Lock()
		for _, timer := range loop.timers {
			if !found || timer.at < next {
				next, found = timer.at, true
			}
		}
		loop.mu.Unlock()
		if !found || next > end {
			break
		}
		loop.AdvanceAsync(next - loop.Now())
		settle()
	}
	loop.AdvanceAsync(end - loop.Now())
	settle()
}

// expectCollected is nexttest.ExpectCollected for objects whose last users are tasks winding down (a closed host's drain,
// a watch's end): the collection is waited for, as the tasks hand off, never assumed after a fixed number of cycles.
func expectCollected[T any](t testing.TB, refs ...weak.Pointer[T]) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		runtime.GC()
		held := -1
		for i, ref := range refs {
			if ref.Value() != nil {
				held = i
				break
			}
		}
		if held < 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Errorf("referent %d was not collected", held)
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
}
