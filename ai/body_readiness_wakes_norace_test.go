//go:build !race

package ai

import (
	"io"
	"runtime"
	"runtime/debug"
	"runtime/metrics"
	"sync/atomic"
	"testing"
)

// endlessBody returns a short chunk on every read and never ends.
type endlessBody struct{}

func (endlessBody) Read(buffer []byte) (int, error) { return copy(buffer, `data: {"x":1}`+"\n\n"), nil }

// schedulerWakeSamples is the runtime's count of goroutines that went back to running: /sched/latencies records one in every eight times a goroutine stops running and resumes. A coroutine switch is not a scheduler wake and is not counted.
func schedulerWakeSamples(t *testing.T) uint64 {
	t.Helper()
	sample := []metrics.Sample{{Name: "/sched/latencies:seconds"}}
	metrics.Read(sample)
	if sample[0].Value.Kind() != metrics.KindFloat64Histogram {
		t.Skip("the runtime does not report /sched/latencies")
	}
	var total uint64
	for _, count := range sample[0].Value.Float64Histogram().Counts {
		total += count
	}
	return total
}

// wakeSamples runs window and returns the goroutine wakes the runtime counted while it ran. The runtime counts the whole process, and a collection in
// progress wakes goroutines of its own: a goroutine that allocates while the collector marks, and cannot repay its assist debt from the workers' credit,
// parks until a worker has credit for it and parks again at its next allocation, and the sweeper yields between batches of spans. None of that is the
// code under test: a traced window of a loaded process counted the wakes of this test's own goroutines parked in "GC mark assist wait for work", and one
// macOS run counted 212 samples, about 1700 wakes, in 4000 reads that wake no goroutine. The window therefore runs with the collector off: runtime.GC
// ends a collection in progress and leaves the heap swept, SetGCPercent(-1) keeps the next one from starting (and waits out one that started in
// between), and a window allocates kilobytes. The collector goes off only after the forced collection, which the pacer still slows an allocator down for;
// with it off the pacer does not. Parallel top-level tests wait until every serial test has ended, so none of them allocates meanwhile, and a memory limit
// headroom above what the runtime has mapped is the backstop for anything that does: the collector runs again at it. Both settings are restored when the
// window ends and when a failed check exits the goroutine inside it.
func wakeSamples(t *testing.T, window func()) uint64 {
	t.Helper()
	const headroom = 256 << 20
	runtime.GC()
	mapped := []metrics.Sample{{Name: "/memory/classes/total:bytes"}}
	metrics.Read(mapped)
	defer debug.SetMemoryLimit(debug.SetMemoryLimit(min(debug.SetMemoryLimit(-1), int64(mapped[0].Value.Uint64())+headroom)))
	defer debug.SetGCPercent(debug.SetGCPercent(-1))
	before := schedulerWakeSamples(t)
	window()
	return schedulerWakeSamples(t) - before
}

func noBodyDelivery(bodyReadSignal) { panic("a ready read must not call deliver") }

// A read the transport's buffers serve completes on the caller's turn: the bridge hands it to no goroutine and allocates nothing but the chunk.
func TestObservedBodyReadinessBufferedReadWakesNoGoroutine(t *testing.T) {
	body := &observedResponseBody{ReadCloser: io.NopCloser(endlessBody{}), owner: &bodyReadOwner{}}
	readiness := newObservedBodyReadiness(body)
	defer func() { _ = readiness.Close() }()
	read := func() {
		if result, ready := readiness.begin(noBodyDelivery); !ready || len(result.data) == 0 {
			t.Fatalf("buffered read = %q ready=%t", result.data, ready)
		}
	}
	read()
	const reads = 4000
	// The round trip through a reader goroutine wakes two goroutines per read, about reads/4 samples.
	samples := wakeSamples(t, func() {
		for range reads {
			read()
		}
	})
	t.Logf("%d buffered reads: %d wake samples", reads, samples)
	if samples > reads/40 {
		t.Fatalf("%d buffered reads woke goroutines %d times (sampled 1 in 8), want none", reads, samples)
	}
	if allocs := testing.AllocsPerRun(200, read); allocs > 1 {
		t.Fatalf("a buffered read allocates %.0f times, want only the chunk", allocs)
	}
}

// A body source reads into a buffer of its own: a garbage collection between two reads of one stream, which would empty a shared pool, does not
// cost the next read a new read-size buffer.
func TestBodyReadinessReadBufferSurvivesGC(t *testing.T) {
	for _, source := range []struct {
		name      string
		readiness bodyReadiness
	}{
		{"observed", newObservedBodyReadiness(&observedResponseBody{ReadCloser: io.NopCloser(endlessBody{}), owner: &bodyReadOwner{}})},
		{"opaque", newOpaqueBodyReadiness(io.NopCloser(endlessBody{}))},
	} {
		delivered := make(chan bodyReadSignal, 1)
		deliver := func(result bodyReadSignal) { delivered <- result }
		read := func() {
			result, ready := source.readiness.begin(deliver)
			if !ready {
				result = <-delivered
			}
			if len(result.data) == 0 {
				t.Fatalf("%s: read %q", source.name, result.data)
			}
		}
		read()
		const reads = 20
		var allocated uint64
		for range reads {
			// The second collection drops what the first moved to a pool's victim cache.
			runtime.GC()
			runtime.GC()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			read()
			runtime.ReadMemStats(&after)
			allocated += after.TotalAlloc - before.TotalAlloc
		}
		_ = source.readiness.Close()
		t.Logf("%s: %d bytes allocated per read after a collection", source.name, allocated/reads)
		if perRead := allocated / reads; perRead >= bodyReadSize/2 {
			t.Errorf("%s: a read after a collection allocates %d bytes, want less than half the %d-byte read buffer", source.name, perRead, bodyReadSize)
		}
	}
}

// A read that waits for the network wakes the goroutine that waits for it and, once, the consumer it delivers to; it does not round-trip through a reader goroutine to learn it is pending, nor hand the completion to a second goroutine.
func TestObservedBodyReadinessPendingReadWakesOneGoroutine(t *testing.T) {
	body := &observedResponseBody{ReadCloser: io.NopCloser(endlessBody{}), owner: &bodyReadOwner{}, alwaysPending: true}
	readiness := newObservedBodyReadiness(body)
	defer func() { _ = readiness.Close() }()
	delivered := make(chan bodyReadSignal, 1)
	deliver := func(result bodyReadSignal) { delivered <- result }
	read := func() {
		if _, ready := readiness.begin(deliver); ready {
			t.Fatal("an always-pending body reported a ready read")
		}
		if result := <-delivered; len(result.data) == 0 {
			t.Fatalf("pending read delivered %q", result.data)
		}
	}
	read()
	const reads = 4000
	// Two wakes per read (the waiting goroutine and this consumer) are reads/4 samples; the request and pending-signal round trip and the hand-off of the completion to a second goroutine add more.
	samples := wakeSamples(t, func() {
		for range reads {
			read()
		}
	})
	t.Logf("%d pending reads: %d wake samples", reads, samples)
	if samples > reads*5/16 {
		t.Fatalf("%d pending reads woke goroutines %d times (sampled 1 in 8), want at most 2 per read", reads, samples)
	}
}

// A window of wakeSamples runs with no collection, however much garbage the rest of the process makes: a goroutine that allocates as fast as it can
// starts a collection every few megabytes, and the wakes of each would count against the code under test.
func TestWakeSamplesWindowRunsWithoutACollection(t *testing.T) {
	var allocated atomic.Int64
	var live atomic.Pointer[[]byte]
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			garbage := make([]byte, 64<<10)
			live.Store(&garbage)
			allocated.Add(int64(len(garbage)))
		}
	}()
	defer func() {
		close(stop)
		<-done
	}()
	collections := func() uint64 {
		sample := []metrics.Sample{{Name: "/gc/cycles/total:gc-cycles"}}
		metrics.Read(sample)
		return sample[0].Value.Uint64()
	}
	var during uint64
	wakeSamples(t, func() {
		started := collections()
		for target := allocated.Load() + 64<<20; allocated.Load() < target; {
			runtime.Gosched()
		}
		during = collections() - started
	})
	if during != 0 {
		t.Fatalf("%d collections ran in a window that allocated 64 MiB, want none", during)
	}
}

// The collector and the memory limit are back as they were when a window ends, whether its work returned or a failed check ended the test goroutine inside
// it (t.Fatal exits the goroutine), and a window runs under a limit that bounds the heap.
func TestWakeSamplesWindowRestoresTheCollector(t *testing.T) {
	const percent, limit = 150, 64 << 30
	defer debug.SetGCPercent(debug.SetGCPercent(percent))
	defer debug.SetMemoryLimit(debug.SetMemoryLimit(limit))
	var inside int64
	for name, window := range map[string]func(){
		"returns":             func() { inside = debug.SetMemoryLimit(-1) },
		"exits the goroutine": runtime.Goexit,
	} {
		ended := make(chan struct{})
		go func() {
			defer close(ended)
			wakeSamples(t, window)
		}()
		<-ended
		if got := debug.SetGCPercent(percent); got != percent {
			t.Fatalf("GC percent is %d after a window that %s, want %d", got, name, percent)
		}
		if got := debug.SetMemoryLimit(limit); got != limit {
			t.Fatalf("memory limit is %d after a window that %s, want %d", got, name, limit)
		}
	}
	if inside <= 0 || inside >= limit {
		t.Fatalf("a window ran under a memory limit of %d, want one below %d that bounds the heap", inside, limit)
	}
}

func BenchmarkObservedBodyReadiness(b *testing.B) {
	b.Run("buffered", func(b *testing.B) {
		readiness := newObservedBodyReadiness(&observedResponseBody{ReadCloser: io.NopCloser(endlessBody{}), owner: &bodyReadOwner{}})
		defer func() { _ = readiness.Close() }()
		b.ReportAllocs()
		for b.Loop() {
			readiness.begin(noBodyDelivery)
		}
	})
	b.Run("pending", func(b *testing.B) {
		readiness := newObservedBodyReadiness(&observedResponseBody{ReadCloser: io.NopCloser(endlessBody{}), owner: &bodyReadOwner{}, alwaysPending: true})
		defer func() { _ = readiness.Close() }()
		delivered := make(chan bodyReadSignal, 1)
		deliver := func(result bodyReadSignal) { delivered <- result }
		b.ReportAllocs()
		for b.Loop() {
			readiness.begin(deliver)
			<-delivered
		}
	})
}
