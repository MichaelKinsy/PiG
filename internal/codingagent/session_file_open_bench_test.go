//go:build unix

package codingagent

import (
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"
)

// processCPU is the CPU time, user and system, the process has used.
func processCPU(tb testing.TB) time.Duration {
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		tb.Fatal(err)
	}
	return time.Duration(usage.Utime.Nano() + usage.Stime.Nano())
}

// BenchmarkOpenRealSession opens the session file LW_OPEN_SESSION names (for example a `sessionscale gen -profile p1` or `longsession gen`
// fixture, or a copy of a real session) the way resume does, and reports the CPU time the open used (cpu-ns/op) and the heap the opened
// Session keeps (retained-B) next to the time and the bytes allocated.
func BenchmarkOpenRealSession(b *testing.B) {
	path := os.Getenv("LW_OPEN_SESSION")
	if path == "" {
		b.Skip("LW_OPEN_SESSION names no session file")
	}
	info, err := os.Stat(path)
	if err != nil {
		b.Fatal(err)
	}
	dir := filepath.Dir(path)
	b.SetBytes(info.Size())
	b.ReportAllocs()
	var retained uint64
	var cpu time.Duration
	for b.Loop() {
		b.StopTimer()
		runtime.GC()
		var before runtime.MemStats
		runtime.ReadMemStats(&before)
		start := processCPU(b)
		b.StartTimer()
		session, err := NewSessionManagerWithDir(dir, dir).Open(path)
		if err != nil {
			b.Fatal(err)
		}
		b.StopTimer()
		cpu += processCPU(b) - start
		runtime.GC()
		var after runtime.MemStats
		runtime.ReadMemStats(&after)
		retained = max(retained, after.HeapAlloc-min(after.HeapAlloc, before.HeapAlloc))
		runtime.KeepAlive(session)
		if session.snapshot != nil {
			session.snapshot.close()
		}
		b.StartTimer()
	}
	b.ReportMetric(float64(retained), "retained-B")
	b.ReportMetric(float64(cpu.Nanoseconds())/float64(b.N), "cpu-ns/op")
}

// BenchmarkTreeFirstSearchRealSession opens /tree on the session LW_OPEN_SESSION names, freshly opened for each round (not timed), as the
// /tree command does (first screen, then the rows' search texts warmed before input is read), and types the query the session-scale gate
// types ("retry_100"), so each round is the first search of a resumed session. It reports the wall and CPU time of opening /tree to its
// first paint (first-paint-ns, first-paint-cpu-ns) and from there to the query's results (query-ns, query-cpu-ns).
func BenchmarkTreeFirstSearchRealSession(b *testing.B) {
	path := os.Getenv("LW_OPEN_SESSION")
	if path == "" {
		b.Skip("LW_OPEN_SESSION names no session file")
	}
	dir := filepath.Dir(path)
	b.ReportAllocs()
	var tree, treeCPU, query, queryCPU time.Duration
	for b.Loop() {
		b.StopTimer()
		session, err := NewSessionManagerWithDir(dir, dir).Open(path)
		if err != nil {
			b.Fatal(err)
		}
		runtime.GC()
		start, startCPU := time.Now(), processCPU(b)
		b.StartTimer()
		roots, lt := session.lazyTree()
		selector := newLazyTreeSelectorComponent(roots, lt, session.GetLeafID(), 40, nil, "default")
		selector.Render(120)
		opened, openedCPU := time.Now(), processCPU(b)
		selector.GetTreeList().WarmSearchTexts()
		for _, key := range "retry_100" {
			selector.HandleInput(string(key))
			selector.Render(120)
		}
		b.StopTimer()
		end, endCPU := time.Now(), processCPU(b)
		tree, treeCPU = tree+opened.Sub(start), treeCPU+openedCPU-startCPU
		query, queryCPU = query+end.Sub(opened), queryCPU+endCPU-openedCPU
		if session.snapshot != nil {
			session.snapshot.close()
		}
		b.StartTimer()
	}
	n := float64(b.N)
	b.ReportMetric(float64(tree.Nanoseconds())/n, "first-paint-ns")
	b.ReportMetric(float64(treeCPU.Nanoseconds())/n, "first-paint-cpu-ns")
	b.ReportMetric(float64(query.Nanoseconds())/n, "query-ns")
	b.ReportMetric(float64(queryCPU.Nanoseconds())/n, "query-cpu-ns")
}
