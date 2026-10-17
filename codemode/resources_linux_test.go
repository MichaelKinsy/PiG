//go:build linux

package codemode

import (
	"os"
	"runtime"
	"runtime/debug"
	"runtime/metrics"
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

// goMemory reports the Go heap marked live by the last collection and the memory the Go runtime holds from the OS:
// everything it mapped minus the heap spans it released.
func goMemory() (live, held int64) {
	samples := []metrics.Sample{
		{Name: "/gc/heap/live:bytes"},
		{Name: "/memory/classes/total:bytes"},
		{Name: "/memory/classes/heap/released:bytes"},
	}
	metrics.Read(samples)
	return int64(samples[0].Value.Uint64()), int64(samples[1].Value.Uint64() - samples[2].Value.Uint64())
}

func returnToBaseline() {
	runtime.GC()
	debug.FreeOSMemory()
}

// Concurrent executions each hold a VM whose linear memory is one Go heap allocation (wazero runs without a custom
// allocator) reserved at the heap limit plus headroom, 320 MiB here, of which the script touches 64 MiB. After they
// finish:
//   - no VM is open and the descriptors are back, so every instance was closed;
//   - the Go heap a full collection finds live is back within 16 MiB of the start, so nothing still reaches a closed
//     instance, a stopped timer included (it stays in an idle P's timer heap until that P runs): one would be 320 MiB;
//   - the memory the Go runtime holds from the OS is back within 64 MiB, so the freed linear memories were returned to
//     the OS rather than kept as free spans;
//   - resident memory is within 200 MiB of the start. The checks before it fix all Go and wasm memory, so this bound
//     only catches memory outside the Go runtime's accounting. The race detector keeps shadow memory of freed
//     instances, so it applies to builds without it.
//
// None of the margins grows with the number of executions.
func TestConcurrentExecutionsReturnMemoryAndDescriptors(t *testing.T) {
	sandbox, err := NewSandbox(SandboxOptions{TimeoutMs: 60_000, MemoryLimitBytes: 256 << 20})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sandbox.Close() })
	checkConcurrentExecutionsReturnMemoryAndDescriptors(t, sandbox)
}

// The same for the inline launcher, whose channel holds the session and whose halt guard's timer reaches the channel.
func TestConcurrentInlineExecutionsReturnMemoryAndDescriptors(t *testing.T) {
	sandbox, err := NewInlineSandbox(InlineSandboxOptions{SandboxOptions: SandboxOptions{TimeoutMs: 60_000, MemoryLimitBytes: 256 << 20}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sandbox.Close() })
	checkConcurrentExecutionsReturnMemoryAndDescriptors(t, sandbox.Sandbox)
}

func checkConcurrentExecutionsReturnMemoryAndDescriptors(t *testing.T, sandbox *Sandbox) {
	t.Helper()
	const script = "const a = new Uint8Array(64 * 1024 * 1024).fill(1); await null; return a.length"
	if r, _ := sandbox.Execute(t.Context(), "return 1", ExecuteOptions{}); !r.OK {
		t.Fatalf("warm-up: %+v", r.Error)
	}
	returnToBaseline()
	baseRSS, baseFDs := rssBytes(t), openFDs(t)
	baseLive, baseHeld := goMemory()
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
		// Execute returns once the VM is asked to stop; measure after the VMs exit.
		exitsWithin(t, terminationGrace, "executions")
		close(stop)
		<-sampled
		returnToBaseline()
		endRSS, endFDs := rssBytes(t), openFDs(t)
		endLive, endHeld := goMemory()
		t.Logf("concurrency %2d: live heap %+d KiB, held %+d KiB, peak RSS +%d MiB, end RSS %+d MiB, descriptors %d -> %d",
			n, (endLive-baseLive)>>10, (endHeld-baseHeld)>>10, (peak-baseRSS)>>20, (endRSS-baseRSS)>>20, baseFDs, endFDs)
		if endFDs > baseFDs+2 {
			t.Errorf("descriptors grew from %d to %d", baseFDs, endFDs)
		}
		if grown := endLive - baseLive; grown > 16<<20 {
			t.Errorf("live Go heap grew by %d MiB after %d executions finished: a closed VM is still reachable", grown>>20, n)
		}
		if grown := endHeld - baseHeld; grown > 64<<20 {
			t.Errorf("Go runtime holds %d MiB more from the OS after %d executions finished", grown>>20, n)
		}
		if grown := endRSS - baseRSS; !raceEnabled && grown > 200<<20 {
			t.Errorf("resident memory grew by %d MiB after %d executions finished", grown>>20, n)
		}
	}
}
