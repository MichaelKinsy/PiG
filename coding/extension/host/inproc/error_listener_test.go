package inproc_test

import (
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// fireErrorForTest is a thin wrapper around the test-only seam in
// export_test.go. Lets test files refer to a lowercase identifier.
func fireErrorForTest(r *inproc.Runner, err *extension.ExtensionError) {
	inproc.FireErrorForTest(r, err)
}

// unmarshalJSON is a thin wrapper to avoid importing encoding/json in
// every test file.
func unmarshalJSON(data []byte, v any) error { return json.Unmarshal(data, v) }

// TestAddErrorListener_NilIsNoOp: nil listener registration is safe and
// returns a no-op unsubscriber. Defensive: pig extension authors may
// pass an unset variable.
func TestAddErrorListener_NilIsNoOp(t *testing.T) {
	r := inproc.NewRunner(nil, ".")
	unsub := r.AddErrorListener(nil)
	if unsub == nil {
		t.Errorf("unsubscriber for nil listener = nil, want no-op fn")
	}
	unsub() // must not panic
}

// TestEmitError_FiresAllRegisteredListeners: 3 listeners registered, all
// 3 fire on a single emitError call.
//
// upstream: runner.ts:479-483 (emitError iterates the entire Set)
func TestEmitError_FiresAllRegisteredListeners(t *testing.T) {
	r := inproc.NewRunner(nil, ".")
	var counts [3]atomic.Int32
	for i := range counts {
		r.AddErrorListener(func(*extension.ExtensionError) { counts[i].Add(1) })
	}

	// emitError is package-private; tests in inproc_test exercise it
	// indirectly via TestEmitErrorRoute helpers added in (tool
	// dispatch): but for we expose a test-only fire method
	// via the white-box helper in helpers_test.go.
	fireErrorForTest(r, &extension.ExtensionError{
		ExtensionPath: "/ext/a",
		Event:         "tool_call",
		Error:         "boom",
	})

	for i := range counts {
		if got := counts[i].Load(); got != 1 {
			t.Errorf("listener[%d] fired %d times, want 1", i, got)
		}
	}
}

// TestUnsubscribe_RemovesOnlyTargetListener: calling the unsubscriber
// for one listener leaves the others firing.
func TestUnsubscribe_RemovesOnlyTargetListener(t *testing.T) {
	r := inproc.NewRunner(nil, ".")
	var a, b, c atomic.Int32
	r.AddErrorListener(func(*extension.ExtensionError) { a.Add(1) })
	unsubB := r.AddErrorListener(func(*extension.ExtensionError) { b.Add(1) })
	r.AddErrorListener(func(*extension.ExtensionError) { c.Add(1) })

	unsubB()

	fireErrorForTest(r, &extension.ExtensionError{Event: "x"})

	if a.Load() != 1 || c.Load() != 1 {
		t.Errorf("a=%d c=%d, want both 1", a.Load(), c.Load())
	}
	if b.Load() != 0 {
		t.Errorf("b fired %d times after unsubscribe, want 0", b.Load())
	}
}

// TestUnsubscribe_IsIdempotent: calling the unsubscriber twice doesn't
// double-remove or panic.
func TestUnsubscribe_IsIdempotent(t *testing.T) {
	r := inproc.NewRunner(nil, ".")
	var a atomic.Int32
	unsub := r.AddErrorListener(func(*extension.ExtensionError) { a.Add(1) })
	unsub()
	unsub() // second call must be a no-op

	fireErrorForTest(r, &extension.ExtensionError{Event: "x"})
	if a.Load() != 0 {
		t.Errorf("listener fired %d times after double-unsub, want 0", a.Load())
	}
}

func TestAddErrorListener_DuplicateRegistrationsAreIndependent(t *testing.T) {
	r := inproc.NewRunner(nil, ".")
	var calls atomic.Int32
	fn := func(*extension.ExtensionError) { calls.Add(1) }

	unsub1 := r.AddErrorListener(fn)
	r.AddErrorListener(fn) // distinct registration
	fireErrorForTest(r, &extension.ExtensionError{Event: "x"})
	if got := calls.Load(); got != 2 {
		t.Errorf("after both registrations: calls = %d, want 2 (distinct entries)", got)
	}

	calls.Store(0)
	unsub1()
	fireErrorForTest(r, &extension.ExtensionError{Event: "x"})
	if got := calls.Load(); got != 1 {
		t.Errorf("after unsub of first: calls = %d, want 1 (second still registered)", got)
	}
}

// TestEmitError_NilErrorIsNoOp: emitError(nil) doesn't panic and doesn't
// fire listeners.
func TestEmitError_NilErrorIsNoOp(t *testing.T) {
	r := inproc.NewRunner(nil, ".")
	var fired atomic.Int32
	r.AddErrorListener(func(*extension.ExtensionError) { fired.Add(1) })

	fireErrorForTest(r, nil)

	if fired.Load() != 0 {
		t.Errorf("nil emitError fired listener %d times, want 0", fired.Load())
	}
}

func TestEmitError_ListenerPanicStopsDispatch(t *testing.T) {
	r := inproc.NewRunner(nil, ".")
	var first, subsequent atomic.Int32
	r.AddErrorListener(func(*extension.ExtensionError) { first.Add(1) })
	r.AddErrorListener(func(*extension.ExtensionError) { panic("intentional") })
	r.AddErrorListener(func(*extension.ExtensionError) { subsequent.Add(1) })

	defer func() {
		if recovered := recover(); recovered != "intentional" {
			t.Errorf("panic = %v, want intentional", recovered)
		}
		if first.Load() != 1 {
			t.Errorf("first listener fired %d times, want 1", first.Load())
		}
		if subsequent.Load() != 0 {
			t.Errorf("subsequent listener fired %d times, want 0", subsequent.Load())
		}
	}()
	fireErrorForTest(r, &extension.ExtensionError{Event: "x"})
	t.Fatal("emitError did not propagate listener panic")
}

// TestAddErrorListener_ConcurrentAddIsRaceClean: 100 goroutines each
// register a listener; final count is 100. Run with -race.
func TestAddErrorListener_ConcurrentAddIsRaceClean(t *testing.T) {
	r := inproc.NewRunner(nil, ".")
	var fired atomic.Int32
	var wg sync.WaitGroup
	const N = 100

	for range N {
		wg.Go(func() {
			r.AddErrorListener(func(*extension.ExtensionError) { fired.Add(1) })
		})
	}
	wg.Wait()

	fireErrorForTest(r, &extension.ExtensionError{Event: "x"})

	if got := fired.Load(); got != N {
		t.Errorf("after %d concurrent AddErrorListener: fired = %d, want %d", N, got, N)
	}
}

// TestEmitError_ConcurrentWithSubscribeIsRaceClean: emitError firing
// while AddErrorListener calls are in flight. Run with -race; the
// snapshot copy in emitError prevents iteration over a mutating slice.
func TestEmitError_ConcurrentWithSubscribeIsRaceClean(t *testing.T) {
	r := inproc.NewRunner(nil, ".")
	var fired atomic.Int32

	// Background subscribers
	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			r.AddErrorListener(func(*extension.ExtensionError) { fired.Add(1) })
		})
	}

	// Concurrent fires
	for range 50 {
		wg.Go(func() {
			fireErrorForTest(r, &extension.ExtensionError{Event: "x"})
		})
	}

	wg.Wait()
	// We can't assert exact count (subscribe/fire interleaving), only
	// that we got here without -race fault.
}

// TestExtensionError_JSONTagsMatchUpstream: locks the wire-format field
// names against drift. JSON tags must be camelCase per upstream
// types.ts:1537-1542; jsontag_parity_test enforces this for events but
// ExtensionError isn't an event so it needs its own gate.
func TestExtensionError_JSONTagsMatchUpstream(t *testing.T) {
	// All four upstream field names. If a Go field's JSON tag drifts,
	// the round-trip below fails.
	in := `{"extensionPath":"/ext/a","event":"tool_call","error":"boom","stack":"line1\nline2"}`
	var got extension.ExtensionError
	if err := unmarshalJSON([]byte(in), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.ExtensionPath != "/ext/a" {
		t.Errorf("ExtensionPath = %q, want /ext/a", got.ExtensionPath)
	}
	if got.Event != "tool_call" {
		t.Errorf("Event = %q, want tool_call", got.Event)
	}
	if got.Error != "boom" {
		t.Errorf("Error = %q, want boom", got.Error)
	}
	if got.Stack != "line1\nline2" {
		t.Errorf("Stack = %q, want line1\\nline2", got.Stack)
	}
}
