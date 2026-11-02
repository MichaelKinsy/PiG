package chordctx_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
)

// countingSignal is a context whose context.AfterFunc registrations are counted, as a listener on an AbortSignal is.
type countingSignal struct {
	context.Context
	listeners atomic.Int64
}

func (s *countingSignal) AfterFunc(f func()) func() bool {
	s.listeners.Add(1)
	return func() bool { s.listeners.Add(-1); return true }
}

// Pi's withAbortSignal(signal, context) of a context without an abort signal is the context with `signal` as its signal:
// no listener on it, whatever the number of contexts derived from it. A call's signal outlives its calls, so a listener per
// derived context would accumulate.
func TestWithAbortSignalOfAParentWithoutCancellationListensToNothingAndFollowsTheSignal(t *testing.T) {
	key := chordctx.NewKey[string]("test.key")
	parent := chordctx.WithValue(chordctx.Background(), key, "parent's")
	counting := &countingSignal{Context: context.Background()}
	for range 100 {
		_ = chordctx.WithAbortSignal(parent, counting)
	}
	if got := counting.listeners.Load(); got != 0 {
		t.Fatalf("listeners on the signal = %d, want none", got)
	}

	signal, abort := chordctx.WithCancel(chordctx.Background())
	derived := chordctx.WithAbortSignal(parent, signal)
	if value, ok := chordctx.Value(derived, key); !ok || value != "parent's" {
		t.Fatalf("value = %q, %v, want the parent's", value, ok)
	}
	if derived.Err() != nil || derived.Done() != signal.Done() {
		t.Fatalf("derived: err %v, done is the signal's %v", derived.Err(), derived.Done() == signal.Done())
	}
	reason := errors.New("the signal's reason")
	abort(reason)
	<-derived.Done()
	if got := context.Cause(derived); !errors.Is(got, reason) {
		t.Fatalf("cause = %v, want the signal's reason", got)
	}
	if got, want := chordctx.AbortOrder(derived), chordctx.AbortOrder(signal); got == 0 || got != want {
		t.Fatalf("AbortOrder = %d, want the signal's %d", got, want)
	}
	// A context derived from it by the standard library is cancelled with the signal.
	child, cancel := context.WithCancelCause(chordctx.WithAbortSignal(parent, signalOf(t)))
	defer cancel(nil)
	if child.Err() != nil {
		t.Fatal("a child of a live signal is cancelled")
	}
}

func signalOf(t *testing.T) context.Context {
	t.Helper()
	signal, _ := chordctx.WithCancel(chordctx.Background())
	return signal
}

// A signal that is aborted already aborts what is derived from it, with its reason.
func TestWithAbortSignalOfAParentWithoutCancellationOfAnAbortedSignalIsAborted(t *testing.T) {
	signal, abort := chordctx.WithCancel(chordctx.Background())
	reason := errors.New("before")
	abort(reason)
	derived := chordctx.WithAbortSignal(chordctx.Background(), signal)
	if derived.Err() == nil || !errors.Is(context.Cause(derived), reason) {
		t.Fatalf("derived: err %v, cause %v, want aborted with the signal's reason", derived.Err(), context.Cause(derived))
	}
}
