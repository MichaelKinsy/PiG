// SPDX-License-Identifier: MIT

package perfgate

import (
	"runtime"
	"runtime/debug"
	"testing"
)

// Work is the heap allocation of one measured call.
type Work struct {
	Allocs, Bytes float64
}

// Measure runs fn once to warm caches, then once more under GOMAXPROCS=1 and returns the heap allocations of the second call: the steady-state cost of fn, with every sync.Pool it draws on already filled. Like testing.AllocsPerRun, the count is process-wide, so the caller keeps other goroutines idle.
//
// No collection starts during either call. A collection empties every sync.Pool: the first moves a pool's items to its victim cache and the next drops them. A collection that starts between the warm call's Put and the counted call's Get therefore makes the counted call allocate the buffer again (net/http's 32 KiB copy buffer, a 64 KiB body read buffer, a bufio buffer): tens of kilobytes that belong to when the collector happened to run and not to fn, which a ceiling set at the steady-state cost cannot absorb. The collector runs on heap growth, and it runs at any time under load or a small GOGC, so Measure collects once to start from the live heap and then turns the collector off until the counted call returns. The heap grows by what the two calls allocate and is collected afterwards.
func Measure(fn func()) Work {
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	defer debug.SetGCPercent(debug.SetGCPercent(-1))
	runtime.GC()
	fn()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	fn()
	runtime.ReadMemStats(&after)
	return Work{Allocs: float64(after.Mallocs - before.Mallocs), Bytes: float64(after.TotalAlloc - before.TotalAlloc)}
}

// Marginal returns the allocation that each unit adds between run(n) and run(2n), divided by n. A cost that is constant per unit gives the same marginal at every n; a cost that grows with the units already processed (a copy of everything so far per unit) doubles when n doubles.
func Marginal(n int, run func(units int)) Work {
	small := Measure(func() { run(n) })
	large := Measure(func() { run(2 * n) })
	return Work{Allocs: (large.Allocs - small.Allocs) / float64(n), Bytes: (large.Bytes - small.Bytes) / float64(n)}
}

// CheckLinear fails t when the marginal bytes per unit at 2n exceed growth times the marginal at n: the per-unit cost must not grow with the length of what came before. It returns the marginal at n.
func CheckLinear(t testing.TB, id string, n int, growth float64, run func(units int)) Work {
	t.Helper()
	first := Marginal(n, run)
	second := Marginal(2*n, run)
	t.Logf("perfgate: %s marginal per unit at %d: %.1f allocs %.0f B; at %d: %.1f allocs %.0f B", id, n, first.Allocs, first.Bytes, 2*n, second.Allocs, second.Bytes)
	if first.Bytes > 0 && second.Bytes > growth*first.Bytes {
		t.Errorf("perfgate: %s is not linear: each unit costs %.0f B after %d units and %.0f B after %d (limit %.2fx)", id, first.Bytes, n, second.Bytes, 2*n, growth)
	}
	return first
}
