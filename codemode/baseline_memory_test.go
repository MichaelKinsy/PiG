package codemode

import (
	"runtime"
	"testing"
)

// idleHeapBound bounds the Go heap a live Codemode sandbox holds once a small script has run. QuickJS's own
// footprint for such a script is a few MiB; the bound is that plus the compiled module, far under the 256 MiB limit
// that used to be reserved up front (issue #211).
const idleHeapBound = 96 << 20

// allocBound bounds the bytes allocated to start a sandbox and run one small script. Measured before the fix: 359
// MiB (the 256 MiB limit reserved up front); with the module alone, about 44 MiB.
const allocBound = 96 << 20

func totalAlloc() uint64 {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.TotalAlloc
}

func heapInUse() uint64 {
	runtime.GC()
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapAlloc
}

// TestIdleBaselineMemoryStaysSmall guards issue #211: a 256 MiB heap limit must not reserve 256 MiB of linear memory
// for an idle session.
func TestIdleBaselineMemoryStaysSmall(t *testing.T) {
	before := heapInUse()
	startAlloc := totalAlloc()
	sandbox, err := NewSandbox(SandboxOptions{TimeoutMs: 30_000, MemoryLimitBytes: 256 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sandbox.Close() }()
	r, err := sandbox.Execute(t.Context(), "return 1 + 1", ExecuteOptions{})
	if err != nil || !r.OK {
		t.Fatalf("%v %+v", err, r)
	}
	used := heapInUse() - before
	if allocated := totalAlloc() - startAlloc; allocated > allocBound {
		t.Fatalf("idle sandbox allocated %d MiB, want <= %d MiB", allocated>>20, allocBound>>20)
	}
	t.Logf("heap held by idle sandbox after one small script: %d MiB", used>>20)
	if used > idleHeapBound {
		t.Fatalf("idle sandbox holds %d MiB of Go heap, want <= %d MiB", used>>20, idleHeapBound>>20)
	}
}
