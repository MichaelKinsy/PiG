// SPDX-License-Identifier: MIT

package gcpolicy

import (
	"os"
	"runtime"
	"runtime/debug"
	"runtime/metrics"
	"strings"
	"testing"
	"time"
)

func gcPercent() uint64 {
	sample := []metrics.Sample{{Name: "/gc/gogc:percent"}}
	metrics.Read(sample)
	return sample[0].Value.Uint64()
}

func unsetGOGC(t *testing.T) {
	t.Setenv("GOGC", "")
	if err := os.Unsetenv("GOGC"); err != nil {
		t.Fatal(err)
	}
	// /gc/heap/live:bytes is the live heap of the last collection, which an earlier test of this process (-count above 1) may have
	// left at its 80 MB: a watcher's first read would take that for a collection since Apply and fall back at once. A collection of
	// the now small heap makes the first read describe the state the test starts from.
	runtime.GC()
}

// D175: with GOGC unset the target holds over collections of a small heap, and the first collection that finds more than
// FallbackLiveBytes live restores the default for the rest of the process.
func TestApplyFallsBackAboveTheLiveHeapLimit(t *testing.T) {
	unsetGOGC(t)
	defer debug.SetGCPercent(debug.SetGCPercent(DefaultPercent))
	defer Apply()()
	for range 3 {
		runtime.GC()
		time.Sleep(10 * time.Millisecond) // Room for the watcher to read after the collection.
	}
	if got := gcPercent(); got != TargetPercent {
		t.Fatalf("GC percent %d after collections of a small heap, want %d", got, TargetPercent)
	}
	live := make([]byte, FallbackLiveBytes+16<<20)
	deadline := time.Now().Add(10 * time.Second)
	for gcPercent() != DefaultPercent {
		if time.Now().After(deadline) {
			t.Fatalf("GC percent %d with %d bytes live, want %d", gcPercent(), len(live), DefaultPercent)
		}
		runtime.GC()
		time.Sleep(time.Millisecond)
	}
	runtime.KeepAlive(live)
}

// D175: a GOGC in the environment wins; the policy leaves the GC percent alone.
func TestApplyLeavesAUserGOGC(t *testing.T) {
	t.Setenv("GOGC", "50")
	defer debug.SetGCPercent(debug.SetGCPercent(50))
	defer Apply()()
	if got := gcPercent(); got != 50 {
		t.Fatalf("GC percent %d with GOGC=50 set, want 50", got)
	}
}

// D175: the fallback does not wait on the runtime's cleanup goroutines. A cleanup runs when the sweeper reaches its span, on one of at
// most max(GOMAXPROCS/4, 1) goroutines that run the queued cleanups in turn (runtime/mcleanup.go), so a check armed as a cleanup could
// trail the collection that found the large heap by as long as the cleanups ahead of it take. Here every cleanup goroutine is
// blocked before Apply and one collection finds more than FallbackLiveBytes live; with no further collection the percent must still
// become DefaultPercent. A design that reads the live heap from a cleanup leaves it at TargetPercent until a blocked cleanup returns.
func TestApplyFallsBackWithoutACleanup(t *testing.T) {
	unsetGOGC(t)
	defer debug.SetGCPercent(debug.SetGCPercent(DefaultPercent))
	blocked := max(runtime.GOMAXPROCS(0)/4, 1)
	started, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	type blocker struct{ _ [16]byte }
	// Production registers no cleanup (watchLiveHeap polls metrics), so this loop is scaffolding that occupies the runtime's cleanup
	// goroutines, not a model of production. One collection per cleanup: cleanups queued together share a block that one goroutine runs
	// in turn, so each collection's cleanup has to find the goroutines already blocked. The runtime starts one more goroutine only from
	// a later runtime.AddCleanup, once a collection's wake-up found none asleep (runtime/mcleanup.go: wake raises needg, AddCleanup
	// calls createGs), and that wake-up runs some time after runtime.GC returns. The runtime offers no signal for the goroutine
	// starting, so until the blocking cleanup has started the loop registers a cleanup that returns at once, bounded by a deadline.
	for range blocked {
		runtime.AddCleanup(new(blocker), func(struct{}) {
			started <- struct{}{}
			<-release
		}, struct{}{})
		runtime.GC()
		deadline := time.Now().Add(10 * time.Second)
		for ran := false; !ran; {
			select {
			case <-started:
				ran = true
			case <-time.After(time.Millisecond):
				if time.Now().After(deadline) {
					t.Fatalf("a blocking cleanup did not run after a collection with %d cleanup goroutines to block", blocked)
				}
				runtime.AddCleanup(new(blocker), func(struct{}) {}, struct{}{})
			}
		}
	}
	defer Apply()()
	live := make([]byte, FallbackLiveBytes+16<<20)
	runtime.GC() // The only collection after Apply: it finds the heap large.
	deadline := time.Now().Add(5 * time.Second)
	for gcPercent() != DefaultPercent {
		if time.Now().After(deadline) {
			t.Fatalf("GC percent %d, %d bytes live and one collection since Apply, want %d", gcPercent(), len(live), DefaultPercent)
		}
		time.Sleep(time.Millisecond)
	}
	runtime.KeepAlive(live)
}

func watcherRunning() bool {
	buf := make([]byte, 1<<20)
	return strings.Contains(string(buf[:runtime.Stack(buf, true)]), "gcpolicy.Policy.watchLiveHeap")
}

// D175: the watcher is owned by its caller. stop returns only after the goroutine has returned, so a process that calls it before
// its exit record counts no goroutine of the policy; calling it again, or after the fallback ended the watcher, returns at once.
func TestStopWaitsForTheWatcher(t *testing.T) {
	unsetGOGC(t)
	defer debug.SetGCPercent(debug.SetGCPercent(DefaultPercent))
	stop := Apply()
	defer func() { stop() }() // A failed check must not leave the watcher for the tests after it.
	// Apply starts the watcher goroutine; the scheduler may not have run it yet.
	deadline := time.Now().Add(5 * time.Second)
	for !watcherRunning() {
		if time.Now().After(deadline) {
			t.Fatal("no watcher goroutine after Apply")
		}
		time.Sleep(time.Millisecond)
	}
	stop()
	if watcherRunning() {
		t.Fatal("the watcher goroutine is still running after stop returned")
	}
	stop()

	stop = Apply()
	live := make([]byte, FallbackLiveBytes+16<<20)
	runtime.GC()
	deadline = time.Now().Add(5 * time.Second)
	for watcherRunning() {
		if time.Now().After(deadline) {
			t.Fatal("the watcher did not end after the fallback fired")
		}
		time.Sleep(time.Millisecond)
	}
	stop() // Already ended: returns at once.
	runtime.KeepAlive(live)
}

// D175: with GOGC set there is no watcher and stop is a no-op.
func TestStopWithAUserGOGC(t *testing.T) {
	t.Setenv("GOGC", "50")
	defer debug.SetGCPercent(debug.SetGCPercent(50))
	Apply()()
	if watcherRunning() {
		t.Fatal("a watcher is running with GOGC set")
	}
}
