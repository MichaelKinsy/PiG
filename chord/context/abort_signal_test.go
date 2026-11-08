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

// A key of this package is looked up through a WithoutAbortSignal context in its parent, without the allocation stdlib's
// WithoutCancel context makes on every Value call; the key is found, and any other key still goes through stdlib's.
func TestWithoutAbortSignalLooksAKeyOfThisPackageUpInItsParentAndAllocatesNothing(t *testing.T) {
	key := chordctx.NewKey[string]("test.key")
	missing := chordctx.NewKey[string]("test.missing")
	ctx := chordctx.WithoutAbortSignal(chordctx.WithValue(chordctx.Background(), key, "found"))
	if got, ok := chordctx.Value(ctx, key); !ok || got != "found" {
		t.Fatalf("value = %q, %v, want found", got, ok)
	}
	if _, ok := chordctx.Value(ctx, missing); ok {
		t.Fatal("a key the parent does not carry was found")
	}
	if allocated := testing.AllocsPerRun(100, func() {
		_, _ = chordctx.Value(ctx, key)
		_, _ = chordctx.Value(ctx, missing)
	}); allocated != 0 {
		t.Fatalf("a lookup through WithoutAbortSignal allocates %v, want 0", allocated)
	}
	// Cancellation stays ignored and its cause stays none: the parent's cancelCtx is not reachable through it.
	parent, cancel := context.WithCancelCause(chordctx.Background())
	detached := chordctx.WithoutAbortSignal(parent)
	cancel(errors.New("stop"))
	if detached.Err() != nil || context.Cause(detached) != nil {
		t.Fatalf("err = %v, cause = %v, want none", detached.Err(), context.Cause(detached))
	}
}
