//go:build !race

package perfgate

import (
	"runtime/debug"
	"sync"
	"testing"
)

const (
	poolBufferSize = 64 << 10
	// garbagePerCall is several times the heap distance a collection needs under GOGC=1 (at least 1 MiB of allocation), so the collector runs several times while a call allocates it.
	garbagePerCall = 4 << 20
)

var (
	// pooled is shaped like the read buffer of a provider response: one large buffer per call, taken from the pool and returned to it when the call is done with it.
	pooled      = sync.Pool{New: func() any { buffer := make([]byte, poolBufferSize); return &buffer }}
	garbageSink []byte
)

// pooledCall takes a buffer from pooled and returns it, then allocates garbagePerCall bytes of garbage and extra bytes more. Its steady-state cost is the garbage and the extra: the buffer is in the pool from the call before.
func pooledCall(extra int) func() {
	return func() {
		buffer := pooled.Get().(*[]byte)
		(*buffer)[0] = 1
		pooled.Put(buffer)
		for range garbagePerCall / 1024 {
			garbageSink = make([]byte, 1024)
		}
		garbageSink = make([]byte, extra)
	}
}

// A collection between the warm call's Put and the counted call's Get empties the pool, and the counted call then pays for a new buffer that its steady-state cost does not include. Under GOGC=1 the collector runs every megabyte of garbage, so the warm call's own garbage runs it several times after its Put, as a heap that stood just under the collector's trigger when the warm call began runs it once, and Measure's own runtime.GC then runs another: the second collection in a row drops the pool's victim cache. The buffer must not show in the measure.
func TestMeasureExcludesPoolRefillsAfterACollection(t *testing.T) {
	defer debug.SetGCPercent(debug.SetGCPercent(1))
	for run := range 20 {
		work := Measure(pooledCall(0))
		if limit := float64(garbagePerCall + poolBufferSize/2); work.Bytes > limit {
			t.Fatalf("run %d: Measure counted %.0f bytes for a call whose steady-state cost is %d: collections emptied the pool between the warm call and the counted one", run, work.Bytes, garbagePerCall)
		}
	}
}

// Turning the collector off for the measure must not change the cost the measure reports: an allocation of the call's own shows in full.
func TestMeasureCountsTheCallsOwnAllocation(t *testing.T) {
	const extra = 100 << 10
	defer debug.SetGCPercent(debug.SetGCPercent(1))
	for run := range 20 {
		work := Measure(pooledCall(extra))
		if floor := float64(garbagePerCall + extra); work.Bytes < floor {
			t.Fatalf("run %d: Measure counted %.0f bytes for a call that allocates %.0f", run, work.Bytes, floor)
		}
		if limit := float64(garbagePerCall + extra + poolBufferSize/2); work.Bytes > limit {
			t.Fatalf("run %d: Measure counted %.0f bytes for a call that allocates %.0f", run, work.Bytes, float64(garbagePerCall+extra))
		}
	}
}

// The collector is the caller's: Measure restores the percent it found, whatever it was.
func TestMeasureLeavesTheCollectorAsItFoundIt(t *testing.T) {
	defer debug.SetGCPercent(debug.SetGCPercent(37))
	Measure(pooledCall(0))
	if got := debug.SetGCPercent(37); got != 37 {
		t.Fatalf("GC percent after Measure = %d, want 37", got)
	}
}
