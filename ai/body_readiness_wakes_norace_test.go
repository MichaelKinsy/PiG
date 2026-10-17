//go:build !race

package ai

import (
	"io"
	"runtime"
	"runtime/metrics"
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
	before := schedulerWakeSamples(t)
	for range reads {
		read()
	}
	// The round trip through a reader goroutine wakes two goroutines per read, about reads/4 samples.
	samples := schedulerWakeSamples(t) - before
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
	before := schedulerWakeSamples(t)
	for range reads {
		read()
	}
	// Two wakes per read (the waiting goroutine and this consumer) are reads/4 samples; the request and pending-signal round trip and the hand-off of the completion to a second goroutine add more.
	samples := schedulerWakeSamples(t) - before
	t.Logf("%d pending reads: %d wake samples", reads, samples)
	if samples > reads*5/16 {
		t.Fatalf("%d pending reads woke goroutines %d times (sampled 1 in 8), want at most 2 per read", reads, samples)
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
