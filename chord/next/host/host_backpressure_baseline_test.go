package host

import (
	"os"
	"runtime"
	"runtime/metrics"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/next/link"
)

// bpConfig reads BP_N (comma list) and BP_IDS (comma list) of the baseline harness.
func bpList(name, fallback string) []int {
	text := os.Getenv(name)
	if text == "" {
		text = fallback
	}
	var out []int
	for each := range strings.SplitSeq(text, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(each))
		if err == nil {
			out = append(out, n)
		}
	}
	return out
}

// bpRun runs the unmarked churn of n fresh origins over ids identities and reports the backlog.
func bpRun(t *testing.T, n, ids int) {
	spec := specOf(Notes)
	s := rawLinked(t, func(h FacetHost) error { _, err := serveNotes(h, nil); return err }, rawLinkedOptions{Parent: true})
	loop, ok := s.Server.(*chordHost).loop().(*Loop)
	if !ok {
		t.Fatal("not a *Loop")
	}
	hold := func(origin int) *link.HoldMessage {
		return &link.HoldMessage{
			Origin: link.Some(origin), Hold: 0, Type: spec.Name, Version: spec.Version, Fingerprint: spec.Fingerprint,
			Params: mustJSONPart(t, jsonObject("session", "S"+strconv.Itoa(origin%ids))), Address: serverWireAddress,
		}
	}
	// Warm up: what a first run allocates for good.
	for each := 1; each <= 200; each++ {
		s.Peer.Send(hold(each))
		s.Peer.Send(&link.ReleaseMessage{Origin: link.Some(each), Hold: 0})
	}
	syncWithin(t, s.Peer, loop)
	loop.quiesce()
	s.Peer.discard()
	runtime.GC()

	var (
		stop                                   = make(chan struct{})
		done                                   sync.WaitGroup
		peakTasks, peakParked, peakG, peakHeap int
		sample                                 = []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}}
	)
	done.Go(func() {
		for {
			select {
			case <-stop:
				return
			case <-time.After(time.Millisecond):
			}
			loop.mu.Lock()
			tasks, parked := len(loop.inflight), len(loop.parked)
			loop.mu.Unlock()
			metrics.Read(sample)
			heap := int(sample[0].Value.Uint64())
			g := runtime.NumGoroutine()
			peakTasks, peakParked, peakG, peakHeap = max(peakTasks, tasks), max(peakParked, parked), max(peakG, g), max(peakHeap, heap)
			s.Peer.discard()
		}
	})
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	started := time.Now()
	for each := 1; each <= n; each++ {
		s.Peer.Send(hold(1000 + each))
		s.Peer.Send(&link.ReleaseMessage{Origin: link.Some(1000 + each), Hold: 0})
	}
	sent := time.Since(started)
	syncWithin(t, s.Peer, loop)
	read := time.Since(started)
	loop.mu.Lock()
	atRead := len(loop.inflight)
	loop.mu.Unlock()
	loop.quiesce()
	empty := time.Since(started)
	runtime.ReadMemStats(&after)
	close(stop)
	done.Wait()
	runtime.GC()
	var settled runtime.MemStats
	runtime.ReadMemStats(&settled)
	t.Logf("BP n=%d ids=%d sent=%v read=%v tailAfterRead=%v tasksAtRead=%d peakTasks=%d peakParked=%d peakGoroutines=%d peakHeapMiB=%.1f allocs/origin=%.0f heapAfterGCMiB=%.1f",
		n, ids, sent.Round(time.Millisecond), read.Round(time.Millisecond), (empty - read).Round(time.Millisecond), atRead,
		peakTasks, peakParked, peakG, float64(peakHeap)/(1<<20),
		float64(after.Mallocs-before.Mallocs)/float64(n), float64(settled.HeapAlloc)/(1<<20))
}

// TestBackpressureBaseline is the measuring harness for the reader / teardown backlog (BP_N=20000,100000 BP_IDS=1,100).
func TestBackpressureBaseline(t *testing.T) {
	if os.Getenv("BP_N") == "" {
		t.Skip("BP_N not set")
	}
	for _, ids := range bpList("BP_IDS", "1") {
		for _, n := range bpList("BP_N", "20000") {
			t.Run(strconv.Itoa(n)+"x"+strconv.Itoa(ids), func(t *testing.T) { bpRun(t, n, ids) })
		}
	}
}

// TestBackpressureHeapPerPendingEnding measures what a pending ending costs (BP_HEAP_N=20000): the head of the chain parks on
// a native's close, n holds of the same identity are released at once behind it, and the heap and the goroutines the pending
// endings hold are read after a collection.
func TestBackpressureHeapPerPendingEnding(t *testing.T) {
	n := 0
	if list := bpList("BP_HEAP_N", ""); len(list) > 0 {
		n = list[0]
	}
	if n == 0 {
		t.Skip("BP_HEAP_N not set")
	}
	gate := make(chan struct{})
	var open sync.Once
	letGo := func() { open.Do(func() { close(gate) }) }
	defer letGo()
	s := rawLinked(t, func(h FacetHost) error { return serveGatedNotes(h, gate) }, rawLinkedOptions{Parent: true})
	loop := s.Server.(*chordHost).loop().(*Loop)
	spec := specOf(Notes)
	hold := func(origin int) *link.HoldMessage {
		return &link.HoldMessage{
			Origin: link.Some(origin), Hold: 0, Type: spec.Name, Version: spec.Version, Fingerprint: spec.Fingerprint,
			Params: mustJSONPart(t, jsonObject("session", "S")), Address: serverWireAddress,
		}
	}
	s.Peer.Send(hold(1))
	s.Peer.Next("bound")
	s.Peer.Send(&link.ReleaseMessage{Origin: link.Some(1), Hold: 0})
	syncWithin(t, s.Peer, loop)
	loop.quiesce()
	heap := func() (uint64, int) {
		s.Peer.discard()
		runtime.GC()
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		return m.HeapAlloc, runtime.NumGoroutine()
	}
	before, goroutinesBefore := heap()
	for origin := 2; origin < 2+n; origin++ {
		s.Peer.Send(hold(origin))
		s.Peer.Send(&link.ReleaseMessage{Origin: link.Some(origin), Hold: 0})
	}
	syncWithin(t, s.Peer, loop)
	loop.quiesce()
	after, goroutinesAfter := heap()
	t.Logf("BPHEAP pending=%d tasksAlive=%d goroutines+=%d heapKiB/ending=%.2f", n, tasksAlive(loop), goroutinesAfter-goroutinesBefore, float64(after-before)/1024/float64(n))
}
