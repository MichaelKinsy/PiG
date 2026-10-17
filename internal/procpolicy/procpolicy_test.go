// SPDX-License-Identifier: MIT

package procpolicy

import (
	"os"
	"runtime"
	"sync"
	"testing"
)

// capForTest applies the policy as a process without GOMAXPROCS in its environment does, and returns the runtime default width. The
// cleanup lifts the cap and restores the GOMAXPROCS the test started with.
func capForTest(t *testing.T) (defaultProcs int) {
	t.Helper()
	started := runtime.GOMAXPROCS(0)
	t.Cleanup(func() {
		mu.Lock()
		capped.Store(false)
		sections = 0
		mu.Unlock()
		runtime.GOMAXPROCS(started)
	})
	runtime.SetDefaultGOMAXPROCS()
	if defaultProcs = runtime.GOMAXPROCS(0); defaultProcs <= Cap {
		t.Skipf("the runtime default is %d Ps here, at most the cap %d: nothing to widen", defaultProcs, Cap)
	}
	t.Setenv("GOMAXPROCS", "")
	if err := os.Unsetenv("GOMAXPROCS"); err != nil {
		t.Fatal(err)
	}
	Apply()
	if got := runtime.GOMAXPROCS(0); got != Cap {
		t.Fatalf("after Apply GOMAXPROCS = %d, want the cap %d", got, Cap)
	}
	return defaultProcs
}

func TestApplyLeavesAUserGOMAXPROCS(t *testing.T) {
	started := runtime.GOMAXPROCS(0)
	t.Cleanup(func() { runtime.GOMAXPROCS(started) })
	runtime.GOMAXPROCS(Cap + 3)
	t.Setenv("GOMAXPROCS", "5")
	Apply()
	if got := runtime.GOMAXPROCS(0); got != Cap+3 || capped.Load() {
		t.Fatalf("with GOMAXPROCS set: GOMAXPROCS = %d, capped = %v; want %d, false", got, capped.Load(), Cap+3)
	}
	// Uncapped, a section changes nothing.
	narrow := Widen()
	if got := runtime.GOMAXPROCS(0); got != Cap+3 {
		t.Fatalf("an uncapped section ran at %d, want %d", got, Cap+3)
	}
	narrow()
	if got := runtime.GOMAXPROCS(0); got != Cap+3 {
		t.Fatalf("after an uncapped section GOMAXPROCS = %d, want %d", got, Cap+3)
	}
}

// A section runs at the runtime default width, and the cap is back when it closes. Sections nest: the cap returns after the last one.
func TestWidenRunsASectionAtTheDefaultWidth(t *testing.T) {
	defaultProcs := capForTest(t)
	outer := Widen()
	if got := runtime.GOMAXPROCS(0); got != defaultProcs {
		t.Fatalf("in a section GOMAXPROCS = %d, want the default %d", got, defaultProcs)
	}
	inner := Widen()
	inner()
	if got := runtime.GOMAXPROCS(0); got != defaultProcs {
		t.Fatalf("after the inner of two nested sections GOMAXPROCS = %d, want the default %d", got, defaultProcs)
	}
	outer()
	if got := runtime.GOMAXPROCS(0); got != Cap {
		t.Fatalf("after the last section GOMAXPROCS = %d, want the cap %d", got, Cap)
	}
}

// Concurrent sections each see the default width for as long as they are open, and the cap returns once all have closed.
func TestConcurrentSectionsKeepTheDefaultWidthUntilTheLastCloses(t *testing.T) {
	defaultProcs := capForTest(t)
	const goroutines, rounds = 8, 50
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range goroutines {
		wg.Go(func() {
			<-start
			for range rounds {
				narrow := Widen()
				if got := runtime.GOMAXPROCS(0); got != defaultProcs {
					t.Errorf("in a concurrent section GOMAXPROCS = %d, want the default %d", got, defaultProcs)
				}
				runtime.Gosched()
				narrow()
			}
		})
	}
	close(start)
	wg.Wait()
	if got := runtime.GOMAXPROCS(0); got != Cap {
		t.Fatalf("after every section closed GOMAXPROCS = %d, want the cap %d", got, Cap)
	}
	if sections != 0 {
		t.Fatalf("%d sections still counted open", sections)
	}
}

// A section that panics closes through its deferred narrow, so the cap returns.
func TestPanickingSectionRestoresTheCap(t *testing.T) {
	capForTest(t)
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("the section did not panic")
			}
		}()
		defer Widen()()
		panic("section failed")
	}()
	if got := runtime.GOMAXPROCS(0); got != Cap {
		t.Fatalf("after a panicking section GOMAXPROCS = %d, want the cap %d", got, Cap)
	}
}
