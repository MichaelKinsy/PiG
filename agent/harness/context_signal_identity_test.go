package harness

import (
	"context"
	"errors"
	"testing"
)

// upstream: packages/chord/src/context/index.ts:71-75
func TestWithAbortSignalPreservesSoleSignalIdentity(t *testing.T) {
	t.Parallel()
	for _, foreign := range []bool{false, true} {
		name := "harness"
		if foreign {
			name = "standard"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			key := CreateContextKey[string]("parent")
			signalKey := CreateContextKey[string]("signal only")
			parent := WithContextValue(BackgroundContext(), key, "parent value")
			var signal context.Context
			var cancel context.CancelCauseFunc
			if foreign {
				signal, cancel = context.WithCancelCause(BackgroundContext())
			} else {
				signal, cancel = WithCancel(BackgroundContext())
			}
			defer cancel(nil)
			signal = WithContextValue(signal, signalKey, "must not leak")
			derived := WithAbortSignal(parent, signal)
			if derived.Done() != signal.Done() {
				t.Error("sole signal identity changed")
			}
			if value, ok := ContextValue(derived, key); !ok || value != "parent value" {
				t.Errorf("parent value = %q, %t", value, ok)
			}
			if _, ok := ContextValue(derived, signalKey); ok {
				t.Error("signal values leaked into invocation context")
			}
			child, cancelChild := WithCancel(derived)
			defer cancelChild(nil)
			reason := errors.New("sole signal cancelled")
			cancel(reason)
			for _, ctx := range []context.Context{derived, child} {
				if !errors.Is(ctx.Err(), context.Canceled) || !errors.Is(context.Cause(ctx), reason) {
					t.Errorf("cancellation = %v, cause = %v", ctx.Err(), context.Cause(ctx))
				}
				select {
				case <-ctx.Done():
				default:
					t.Error("cancellation was not synchronous")
				}
			}
			if parent.Err() != nil {
				t.Error("signal cancelled the parent")
			}
		})
	}
}

func BenchmarkWithAbortSignalSoleSignal(b *testing.B) {
	key := CreateContextKey[string]("parent")
	parent := WithContextValue(BackgroundContext(), key, "value")
	for b.Loop() {
		signal, cancel := WithCancel(BackgroundContext())
		ctx := WithAbortSignal(parent, signal)
		cancel(nil)
		if ctx.Err() == nil {
			b.Fatal("missing cancellation")
		}
	}
}
