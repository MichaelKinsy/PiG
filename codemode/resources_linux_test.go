//go:build linux

package codemode

import (
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func rssBytes(t *testing.T) int64 {
	t.Helper()
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		t.Fatal(err)
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		if rest, ok := strings.CutPrefix(line, "VmRSS:"); ok {
			kb, err := strconv.ParseInt(strings.Fields(rest)[0], 10, 64)
			if err != nil {
				t.Fatal(err)
			}
			return kb << 10
		}
	}
	t.Fatal("no VmRSS")
	return 0
}

func openFDs(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	return len(entries)
}

func returnToBaseline() {
	runtime.GC()
	debug.FreeOSMemory()
}

// Concurrent executions each hold a VM with a 64 MiB buffer. After they finish, resident memory and descriptors
// return to within a fixed margin of the start; the margin does not grow with the number of executions. The race
// detector keeps shadow memory of freed instances, so the resident-memory margin applies to builds without it.
func TestConcurrentExecutionsReturnMemoryAndDescriptors(t *testing.T) {
	sandbox, err := NewSandbox(SandboxOptions{TimeoutMs: 60_000, MemoryLimitBytes: 256 << 20})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sandbox.Close() })
	const script = "const a = new Uint8Array(64 * 1024 * 1024).fill(1); await null; return a.length"
	if r, _ := sandbox.Execute(t.Context(), "return 1", ExecuteOptions{}); !r.OK {
		t.Fatalf("warm-up: %+v", r.Error)
	}
	returnToBaseline()
	baseRSS, baseFDs := rssBytes(t), openFDs(t)
	for _, n := range []int{1, 4, 16} {
		var peak int64
		stop := make(chan struct{})
		sampled := make(chan struct{})
		go func() {
			defer close(sampled)
			for {
				select {
				case <-stop:
					return
				case <-time.After(5 * time.Millisecond):
					if r := rssBytes(t); r > peak {
						peak = r
					}
				}
			}
		}()
		var wg sync.WaitGroup
		for range n {
			wg.Go(func() {
				r, err := sandbox.Execute(t.Context(), script, ExecuteOptions{})
				if err != nil || !r.OK || string(r.Value) != "67108864" {
					t.Errorf("execution: ok=%v value=%s error=%+v, %v", r.OK, r.Value, r.Error, err)
				}
			})
		}
		wg.Wait()
		close(stop)
		<-sampled
		returnToBaseline()
		endRSS, endFDs := rssBytes(t), openFDs(t)
		t.Logf("concurrency %2d: peak RSS +%d MiB, end RSS +%d MiB, descriptors %d -> %d, live VMs %d",
			n, (peak-baseRSS)>>20, (endRSS-baseRSS)>>20, baseFDs, endFDs, liveVMs.Load())
		if live := liveVMs.Load(); live != 0 {
			t.Errorf("%d live VMs", live)
		}
		if endFDs > baseFDs+2 {
			t.Errorf("descriptors grew from %d to %d", baseFDs, endFDs)
		}
		if grown := endRSS - baseRSS; !raceEnabled && grown > 200<<20 {
			t.Errorf("resident memory grew by %d MiB after %d executions finished", grown>>20, n)
		}
	}
}
