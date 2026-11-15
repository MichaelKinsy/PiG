package chordctx

import (
	"context"
	"runtime"
	"testing"
)

// A listener is held by every source it listens to, and a source that outlives its waits (a link's, a host's context)
// must not keep the waits that ended: not the registrations, and not the capacity the set grew to while they were held.
// 20,000 waits registered at once on one long-lived context and ended each of the three ways leave its set empty (nil: no
// bucket array kept) and the heap where it was: the runtime keeps about 1 MB for the goroutines a derived context's signal
// starts, and waits that stay held are 6 to 8 MB after they ended.
func TestOnAbortLeavesNothingHeldByALongLivedContext(t *testing.T) {
	const waits = 20_000
	heap := func() uint64 {
		var stats runtime.MemStats
		for range 4 {
			runtime.GC()
		}
		runtime.ReadMemStats(&stats)
		return stats.HeapAlloc
	}
	for _, end := range []struct {
		name string
		// register makes one wait on a context under long, and returns what ends it.
		register func(long context.Context) (ends func())
	}{
		{"the result came first: the waiter stops its listener", func(long context.Context) func() {
			stop, _ := OnAbort(long, func(error) {})
			return func() { stop() }
		}},
		{"the awaited context aborted: the listener ran, nobody stops it", func(long context.Context) func() {
			awaited, abort := WithCancel(long)
			OnAbort(awaited, func(error) {})
			return func() { abort(nil) }
		}},
		{"the signal of a derived context aborted: the listener ran, nobody stops it", func(long context.Context) func() {
			signal, abort := WithCancel(Background())
			OnAbort(WithAbortSignal(long, signal), func(error) {})
			return func() { abort(nil) }
		}},
	} {
		t.Run(end.name, func(t *testing.T) {
			long, cancelLong := WithCancel(Background())
			defer cancelLong(nil)
			held := func() (count int, kept bool) {
				source := long.(*abortContext)
				source.mu.Lock()
				defer source.mu.Unlock()
				return len(source.listeners), source.listeners != nil
			}
			before := heap()
			ends := make([]func(), 0, waits)
			for range waits {
				ends = append(ends, end.register(long))
			}
			if count, _ := held(); count != waits {
				t.Fatalf("%d waits registered, the long-lived context holds %d", waits, count)
			}
			for _, each := range ends {
				each()
			}
			if count, kept := held(); count != 0 || kept {
				t.Fatalf("after every wait ended the long-lived context holds %d listeners (set kept: %v), want none", count, kept)
			}
			clear(ends)
			if grown := int64(heap()) - int64(before); grown >= 4<<20 {
				t.Fatalf("the heap grew by %d bytes over %d ended waits, want under 4 MiB", grown, waits)
			}
		})
	}
}
