package host

import (
	"context"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord"
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
	watch, err := ref.State().Watch(background)
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

// waitMain blocks the test's own goroutine until the signal fired.
func (s signal) waitMain(t testing.TB) {
	t.Helper()
	s.wait(t)
}

// refCall is `await ref.method(args, context)`.
func refCall(ref *Reference, method string, args delta.JsonValue) (delta.JsonValue, error) {
	return ref.Call(method, args, background)
}
