package chordctx

import (
	"context"
	"fmt"
	"testing"
)

// The cost of a source's cancel with N contexts derived from it: the link the chord host's per-call contexts and the harness's
// per-operation contexts make. One op is the whole life of a source: derive N contexts from it (WithAbortSignal), cancel it. Run
// by hand (not in CI): go test -run '^$' -bench BenchmarkCancelFanOut -benchmem ./chord/context/
func BenchmarkCancelFanOut(b *testing.B) {
	for _, children := range []int{1, 16, 256} {
		b.Run(fmt.Sprintf("children=%d", children), func(b *testing.B) {
			parent, cancelParent := WithCancel(Background())
			defer cancelParent(nil)
			derived := make([]context.Context, children)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				signal, cancel := WithCancel(Background())
				for i := range derived {
					derived[i] = WithAbortSignal(parent, signal)
				}
				cancel(nil)
				if derived[0].Err() == nil {
					// The cancel returns with every derived context aborted only where this package derives synchronously.
					<-derived[0].Done()
				}
			}
		})
	}
}

// The same with a listener on each derived context, as a wait on it registers one (OnAbort), the listeners run by the cancel.
func BenchmarkCancelFanOutWithListeners(b *testing.B) {
	for _, children := range []int{1, 16, 256} {
		b.Run(fmt.Sprintf("children=%d", children), func(b *testing.B) {
			parent, cancelParent := WithCancel(Background())
			defer cancelParent(nil)
			derived := make([]context.Context, children)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				signal, cancel := WithCancel(Background())
				for i := range derived {
					derived[i] = WithAbortSignal(parent, signal)
					OnAbort(derived[i], func(error) {})
				}
				cancel(nil)
				if derived[children-1].Err() == nil {
					<-derived[children-1].Done()
				}
			}
		})
	}
}

// The life of a call-scoped context without a signal: WithCancel under a live parent, cancelled; the chord host's commonest
// derivation, which the registry must not slow.
func BenchmarkWithCancelCancel(b *testing.B) {
	parent, cancelParent := WithCancel(Background())
	defer cancelParent(nil)
	b.ReportAllocs()
	for range b.N {
		_, cancel := WithCancel(parent)
		cancel(nil)
	}
}
