package lockorder

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func reset(t *testing.T) string {
	t.Helper()
	mu.Lock()
	names = []string{""}
	byName = map[string]class{}
	adj = map[class]map[class]struct{}{}
	seen = map[edge]string{}
	holding = map[int64][]held{}
	reports = map[string]bool{}
	instAdj = map[uintptr]map[uintptr]struct{}{}
	instRev = map[uintptr]map[uintptr]struct{}{}
	instEdge = 0
	classMulti = map[class]bool{}
	classFirst = map[class]uintptr{}
	mu.Unlock()
	path := filepath.Join(t.TempDir(), "report")
	t.Setenv("PIG_LOCKORDER_REPORT", path)
	return path
}

func read(path string) string {
	b, _ := os.ReadFile(path)
	return string(b)
}

// onGoroutine runs f on its own goroutine, so each ordering below is taken by a different goroutine as in a real inversion.
func onGoroutine(f func()) {
	var wg sync.WaitGroup
	wg.Go(f)
	wg.Wait()
}

func TestInversionOfTheSameLocksFails(t *testing.T) {
	report := reset(t)
	var a, b Mutex
	onGoroutine(func() { a.LockAs("a"); b.LockAs("b"); b.Unlock(); a.Unlock() })
	if got := read(report); got != "" {
		t.Fatalf("consistent order reported: %s", got)
	}
	onGoroutine(func() { b.LockAs("b"); a.LockAs("a"); a.Unlock(); b.Unlock() })
	got := read(report)
	if !strings.Contains(got, "lock order cycle") {
		t.Fatalf("ABBA inversion not reported, report = %q", got)
	}
}

func TestConsistentOrderPasses(t *testing.T) {
	report := reset(t)
	var a, b, c Mutex
	for range 3 {
		onGoroutine(func() {
			a.LockAs("a")
			b.LockAs("b")
			c.LockAs("c")
			c.Unlock()
			b.Unlock()
			a.Unlock()
		})
	}
	if got := read(report); got != "" {
		t.Fatalf("report = %q, want none", got)
	}
}

func TestInversionThroughThirdLockFails(t *testing.T) {
	report := reset(t)
	var a, b, c Mutex
	onGoroutine(func() { a.LockAs("a"); b.LockAs("b"); b.Unlock(); a.Unlock() })
	onGoroutine(func() { b.LockAs("b"); c.LockAs("c"); c.Unlock(); b.Unlock() })
	onGoroutine(func() { c.LockAs("c"); a.LockAs("a"); a.Unlock(); c.Unlock() })
	if got := read(report); !strings.Contains(got, "lock order cycle") {
		t.Fatalf("a -> b -> c -> a not reported, report = %q", got)
	}
}

func TestClassCycleOverSeveralInstancesIsOnlyACandidate(t *testing.T) {
	report := reset(t)
	// Two parents and two children of the same classes taking different instances in opposite orders: no pair of objects is ever inverted.
	var a1, a2, b1, b2 Mutex
	onGoroutine(func() { a1.LockAs("a"); b1.LockAs("b"); b1.Unlock(); a1.Unlock() })
	onGoroutine(func() { a2.LockAs("a"); b2.LockAs("b"); b2.Unlock(); a2.Unlock() })
	onGoroutine(func() { b2.LockAs("b"); a1.LockAs("a"); a1.Unlock(); b2.Unlock() })
	if got := read(report); got != "" {
		t.Fatalf("report = %q, want no failure for instance-disjoint orders", got)
	}
	if got := read(report + ".potential"); !strings.Contains(got, "lock order cycle") {
		t.Fatalf("candidate file = %q, want the class cycle", got)
	}
}

// sync.RWMutex prohibits recursive read locking: a writer that queues between the two read locks blocks the second one, and the writer waits for the first.
func TestRecursiveReadLockFails(t *testing.T) {
	report := reset(t)
	var rw RWMutex
	onGoroutine(func() {
		rw.RLockAs("rw")
		rw.RLockAs("rw")
		rw.RUnlock()
		rw.RUnlock()
	})
	if got := read(report); !strings.Contains(got, "read-locks rw again") {
		t.Fatalf("recursive read lock not reported, report = %q", got)
	}
}

// Read locks of one RWMutex on different goroutines do not nest.
func TestConcurrentReadersPass(t *testing.T) {
	report := reset(t)
	var rw RWMutex
	rw.RLockAs("rw")
	onGoroutine(func() { rw.RLockAs("rw"); rw.RUnlock() })
	rw.RUnlock()
	if got := read(report); got != "" {
		t.Fatalf("readers on different goroutines reported: %s", got)
	}
}

func TestLocksStillExclude(t *testing.T) {
	reset(t)
	var m Mutex
	var rw RWMutex
	n := 0
	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			m.Lock()
			rw.Lock()
			n++
			rw.Unlock()
			m.Unlock()
		})
	}
	wg.Wait()
	if n != 50 {
		t.Fatalf("n = %d, want 50", n)
	}
	if !m.TryLock() {
		t.Fatal("TryLock on a free mutex failed")
	}
	if m.TryLock() {
		t.Fatal("TryLock on a held mutex succeeded")
	}
	m.Unlock()
}

// A lock whose object died leaves no order behind: the allocator may hand its address to a new lock, and the edges recorded for the old one must not join the new one into a cycle.
func TestPurgedLockLeavesNoOrderBehind(t *testing.T) {
	report := reset(t)
	var a, b, reborn Mutex
	onGoroutine(func() { a.LockAs("a"); b.LockAs("b"); b.Unlock(); a.Unlock() })
	// What b's finalizer does when b dies, then a new lock that takes the dead one's identity (the same address).
	purge(b.id())
	reborn.ident.Store(b.ident.Load())
	onGoroutine(func() { reborn.LockAs("reborn"); a.LockAs("a"); a.Unlock(); reborn.Unlock() })
	if got := read(report); got != "" {
		t.Fatalf("a purged lock's old order still counts: %s", got)
	}
}

func TestUnpurgedIdentityReuseWouldReportACycle(t *testing.T) {
	report := reset(t)
	var a, b, reborn Mutex
	onGoroutine(func() { a.LockAs("a"); b.LockAs("b"); b.Unlock(); a.Unlock() })
	reborn.ident.Store(b.ident.Load()) // same identity, edges not purged: the situation purge exists to prevent
	onGoroutine(func() { reborn.LockAs("reborn"); a.LockAs("a"); a.Unlock(); reborn.Unlock() })
	if got := read(report); !strings.Contains(got, "same lock objects") {
		t.Fatalf("the shared identity was not treated as one lock, report = %q", got)
	}
}
