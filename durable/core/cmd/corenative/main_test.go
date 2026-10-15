// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

//go:build !wasip1

package main

import (
	"encoding/binary"
	"os"
	"os/exec"
	"runtime"
	"runtime/debug"
	"runtime/metrics"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core"
)

// sink keeps the garbage allocation on the heap until the test drops it.
var sink []byte

// TestMemStatsSlot6IsTheLiveHeapAfterACollection drives op 6 (mem_stats) through serve: slot 6 is the live heap after the
// collection the call runs, so garbage the automatic collector has not reached is not counted; slot 7 is the runtime's
// Sys, never below the heap. A handle that does not exist still gets the process's heap: NativeCore.heapStats asks with
// handle 0.
func TestMemStatsSlot6IsTheLiveHeapAfterACollection(t *testing.T) {
	defer debug.SetGCPercent(debug.SetGCPercent(-1))
	s := &server{sessions: map[uint32]*core.Session{}, next: 1}
	h := binary.LittleEndian.Uint32(s.serve(request{op: 3}))

	const garbage = 64 << 20
	sink = make([]byte, garbage)
	sink[garbage-1] = 1
	sink = nil

	slots := func(handle uint32) [8]uint64 {
		t.Helper()
		out := s.serve(request{op: 6, handle: handle})
		if len(out) != 64 {
			t.Fatalf("mem_stats answered %d bytes, want 64", len(out))
		}
		var m [8]uint64
		for i := range m {
			m[i] = binary.LittleEndian.Uint64(out[i*8:])
		}
		return m
	}
	m := slots(h)
	if m[6] == 0 || m[6] >= garbage/2 {
		t.Fatalf("slot 6 (heap in use) = %d: want the live heap after a collection, without the %d bytes of garbage", m[6], garbage)
	}
	if m[7] < m[6] {
		t.Fatalf("slot 7 (runtime Sys) = %d is below slot 6 (heap) = %d", m[7], m[6])
	}
	if unknown := slots(0); unknown[6] == 0 || unknown[6] >= garbage/2 || unknown[7] < unknown[6] {
		t.Fatalf("handle 0 (no session): heap %d, Sys %d: want the process's live heap and Sys", unknown[6], unknown[7])
	}
	runtime.KeepAlive(s)
}

// TestMemStatsSlot6DropsWhatOnlyAPoolHolds: an object only a sync.Pool holds is garbage, and slot 6 does not count it.
// One collection moves a pool's objects to its victim cache and the next frees them, so after one the heap still held what
// the pools happened to hold when the gate asked, and moved between runs of the same core by a span or more.
func TestMemStatsSlot6DropsWhatOnlyAPoolHolds(t *testing.T) {
	defer debug.SetGCPercent(debug.SetGCPercent(-1))
	s := &server{sessions: map[uint32]*core.Session{}, next: 1}
	const pooled = 16 << 20
	var pool sync.Pool
	b := make([]byte, pooled)
	b[pooled-1] = 1
	pool.Put(&b)
	out := s.serve(request{op: 6})
	if heap := binary.LittleEndian.Uint64(out[6*8:]); heap >= pooled/2 {
		t.Fatalf("slot 6 (heap in use) = %d: want the live heap without the %d bytes only a sync.Pool holds", heap, pooled)
	}
	runtime.KeepAlive(&pool)
}

var allocSink []any

var heapAllocSamples = []metrics.Sample{{Name: "/gc/heap/allocs:bytes"}, {Name: "/gc/heap/allocs:objects"}, {Name: "/gc/heap/tiny/allocs:objects"}}

// Op 9 answers the runtime's TotalAlloc, Mallocs and NumGC (ABI section 4.1). ReadMemStats flushes every P's cached spans,
// so its TotalAlloc is runtime/metrics' /gc/heap/allocs:bytes and its Mallocs /gc/heap/allocs:objects plus
// /gc/heap/tiny/allocs:objects, exact at the read: allocBytes per turn in the count gate is /gc/heap/allocs:bytes per turn.
// runtime/metrics alone does not flush and lags by what the cached spans hold (155 KB of 718 KB after 2,000 small
// allocations, measured with Go 1.27.1), so op 9 keeps ReadMemStats.
func TestAllocStatsAreTheHeapAllocsMetrics(t *testing.T) {
	defer debug.SetGCPercent(debug.SetGCPercent(-1))
	metrics.Read(heapAllocSamples) // the first Read sets up its tables
	for i := range 1000 {
		allocSink = append(allocSink, new(byte), make([]byte, 100+i), &struct{ a, b int }{i, i})
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	metrics.Read(heapAllocSamples)
	b := allocStats() // reads before it allocates its answer
	if len(b) != 24 {
		t.Fatalf("op 9 answered %d bytes, want 24: bytes allocated, allocations and completed collections", len(b))
	}
	got := [3]uint64{binary.LittleEndian.Uint64(b), binary.LittleEndian.Uint64(b[8:]), binary.LittleEndian.Uint64(b[16:])}
	if want := [3]uint64{ms.TotalAlloc, ms.Mallocs, uint64(ms.NumGC)}; got != want {
		t.Errorf("op 9 answered bytes, allocations, collections %v; ReadMemStats has %v", got, want)
	}
	m := [2]uint64{heapAllocSamples[0].Value.Uint64(), heapAllocSamples[1].Value.Uint64() + heapAllocSamples[2].Value.Uint64()}
	if m != [2]uint64{got[0], got[1]} {
		t.Errorf("/gc/heap/allocs:bytes and the allocation objects %v differ from op 9's %v", m, got[:2])
	}
	allocSink = nil
}

// TestAllocStatsOp9CountsCollections drives op 9 (alloc_stats) through serve: three u64, the third the completed
// collections, which reading alloc_stats does not move and a full collection (op 6, mem_stats) does. The host reads the
// third counter at offset 16 (host/cf native.ts), so a two-counter answer breaks every native count run.
func TestAllocStatsOp9CountsCollections(t *testing.T) {
	defer debug.SetGCPercent(debug.SetGCPercent(-1))
	s := &server{sessions: map[uint32]*core.Session{}, next: 1}
	read := func() (bytes, mallocs, gcs uint64) {
		t.Helper()
		out := s.serve(request{op: 9})
		if len(out) != 24 {
			t.Fatalf("alloc_stats answered %d bytes, want 24", len(out))
		}
		return binary.LittleEndian.Uint64(out), binary.LittleEndian.Uint64(out[8:]), binary.LittleEndian.Uint64(out[16:])
	}
	_, _, g0 := read()
	if _, _, g := read(); g != g0 {
		t.Fatalf("reading alloc_stats moved the collection count from %d to %d", g0, g)
	}
	s.serve(request{op: 6})
	b1, m1, g1 := read()
	if g1 <= g0 || b1 == 0 || m1 == 0 {
		t.Fatalf("alloc_stats after mem_stats: %d bytes, %d allocations, %d collections (before %d): want the full collection counted", b1, m1, g1, g0)
	}
}

// Op 9 must flush the per-P cached spans before it reads (ReadMemStats does): runtime/metrics read on its own lags by the
// bytes allocated from spans still cached, so a count read with it would miss part of a turn's allocation. Here op 9
// is the first read after the allocations, and ReadMemStats right after it may differ only by op 9's own answer.
func TestAllocStatsFlushesTheCachedSpans(t *testing.T) {
	defer debug.SetGCPercent(debug.SetGCPercent(-1))
	for i := range 2000 {
		allocSink = append(allocSink, new(byte), make([]byte, 16+i%112))
	}
	b := allocStats()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	if got := binary.LittleEndian.Uint64(b); got > ms.TotalAlloc || ms.TotalAlloc-got > 1<<10 {
		t.Errorf("op 9 answered %d bytes allocated, ReadMemStats right after it %d: op 9 must read after a flush", got, ms.TotalAlloc)
	}
	allocSink = nil
}

// kept48 holds the one object TestHeapThreadsNetsOutAnExtraThread adds to the live heap.
var kept48 []byte

func heapThreadsOf(t *testing.T, s *server) (heap, threads, perThread, before uint64) {
	t.Helper()
	out := s.serve(request{op: 10})
	if len(out) != 32 {
		t.Fatalf("heap_threads answered %d bytes, want 32", len(out))
	}
	u := func(i int) uint64 { return binary.LittleEndian.Uint64(out[i*8:]) }
	return u(0), u(1), u(2), u(3)
}

// TestHeapThreadsNetsOutAnExtraThread drives op 10 through serve in a process started with one P, as the count gate runs
// the core. A thread the
// runtime starts between two requests moves the heap by about perThread (5,320 B on go1.27.2) and leaves heap -
// threads*perThread within `noise`; one more 48 B object kept live moves that figure by 48 within `noise`. The goroutines
// that pin threads start before the first request, so their own objects are in every reading. `noise` is 16 B: in 3 of 40
// runs a one-off 16 B runtime object lands between the readings.
func TestHeapThreadsNetsOutAnExtraThread(t *testing.T) {
	// The gate starts the core with GOMAXPROCS=1; a process that starts with more Ps and drops to one keeps per-P caches
	// whose objects move between the readings, so the test runs in its own process started so.
	if os.Getenv("CORENATIVE_HEAP_THREADS_CHILD") == "" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestHeapThreadsNetsOutAnExtraThread$", "-test.count=1")
		cmd.Env = append(os.Environ(), "CORENATIVE_HEAP_THREADS_CHILD=1", "GOMAXPROCS=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		return
	}
	const noise = 16
	defer debug.SetGCPercent(debug.SetGCPercent(-1))
	s := &server{sessions: map[uint32]*core.Session{}, next: 1}
	const pins = 16
	start, locked, release, done := make([]chan struct{}, pins), make(chan struct{}), make(chan struct{}), make(chan struct{})
	for i := range start {
		start[i] = make(chan struct{})
		go func(start <-chan struct{}) {
			if _, lock := <-start; lock {
				runtime.LockOSThread()
				locked <- struct{}{}
				<-release
				runtime.UnlockOSThread()
			}
			done <- struct{}{}
		}(start[i])
	}
	pinned := 0
	defer func() {
		close(release)
		for i := pinned; i < pins; i++ {
			close(start[i])
		}
		for range pins {
			<-done
		}
	}()

	h0, t0, p, before := heapThreadsOf(t, s)
	if p == 0 || before == 0 || t0 < before {
		t.Fatalf("first request: perThread %d, threads %d (%d before calibration): want a measured perThread and the thread it started", p, t0, before)
	}
	sample := []metrics.Sample{{Name: "/sched/threads/total:threads"}}
	for pinned < pins {
		start[pinned] <- struct{}{}
		<-locked
		pinned++
		if metrics.Read(sample); sample[0].Value.Uint64() > t0 {
			break
		}
	}
	h1, t1, p1, _ := heapThreadsOf(t, s)
	if t1 <= t0 || p1 != p {
		t.Fatalf("after pinning %d goroutines: threads %d -> %d, perThread %d -> %d: want one more thread, the same perThread", pinned, t0, t1, p, p1)
	}
	if moved := int64(h1-t1*p) - int64(h0-t0*p); moved < -noise || moved > noise || h1-h0 < (t1-t0)*p-noise {
		t.Fatalf("heap %d -> %d with threads %d -> %d at %d B each: the heap net of threads moved by %d", h0, h1, t0, t1, p, int64(h1-t1*p)-int64(h0-t0*p))
	}

	kept48 = make([]byte, 48)
	h2, t2, _, _ := heapThreadsOf(t, s)
	if got := int64(h2-t2*p) - int64(h1-t1*p); got < 48-noise || got > 48+noise {
		t.Fatalf("one more 48 B object moved the heap net of threads by %d B", got)
	}
	kept48 = nil
}
