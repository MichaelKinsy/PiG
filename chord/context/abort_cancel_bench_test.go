package chordctx

import (
	"context"
	"testing"
)

// The cost of the cancel function's own work, the path the claim of a cancel is on: by hand (not in CI),
// go test -run '^$' -bench 'BenchmarkCancel' -benchmem ./chord/context/

// A context derived and cancelled with nothing listening: the chord host's commonest derivation.
func BenchmarkCancelOwned(b *testing.B) {
	parent, cancelParent := WithCancel(Background())
	defer cancelParent(nil)
	b.ReportAllocs()
	for range b.N {
		_, cancel := WithCancel(parent)
		cancel(nil)
	}
}

// The same with a listener a wait registered (OnAbort), run by the cancel.
func BenchmarkCancelOwnedWithListener(b *testing.B) {
	parent, cancelParent := WithCancel(Background())
	defer cancelParent(nil)
	b.ReportAllocs()
	for range b.N {
		ctx, cancel := WithCancel(parent)
		OnAbort(ctx, func(error) {})
		cancel(nil)
	}
}

// A cancel called again after the abort: the `defer cancel()` that follows an explicit cancel.
func BenchmarkCancelRepeated(b *testing.B) {
	_, cancel := WithCancel(Background())
	cancel(nil)
	b.ReportAllocs()
	for range b.N {
		cancel(nil)
	}
}

// Many cancels of contexts of one parent at once: the cancel path with the counter of aborts shared.
func BenchmarkCancelParallel(b *testing.B) {
	parent, cancelParent := WithCancel(Background())
	defer cancelParent(context.Canceled)
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, cancel := WithCancel(parent)
			cancel(nil)
		}
	})
}
