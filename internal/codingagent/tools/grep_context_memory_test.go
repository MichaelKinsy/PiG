package tools

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"testing"
)

// Pi #9276: grep.ts getFileLines reads and splits every matched file to print a
// few context lines, so a 100 MB log with context 2 costs several times its size
// in memory. The same output must come from a window over the file.
func TestGrepContextDoesNotLoadTheMatchedFile(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("ripgrep unavailable")
	}
	// The heap is process-wide: goroutines other tests in the package leave behind can allocate during the sample. The
	// measurement runs alone in a fresh test process.
	if os.Getenv("PIG_GREP_HEAP_CHILD") == "" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestGrepContextDoesNotLoadTheMatchedFile$", "-test.v")
		cmd.Env = append(os.Environ(), "PIG_GREP_HEAP_CHILD=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		return
	}
	const lines = 3_000_000
	dir := t.TempDir()
	path := filepath.Join(dir, "server.log")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w := bufio.NewWriter(f)
	for i := 1; i <= lines; i++ {
		if i == 1_000_000 || i == 2_500_000 {
			_, _ = fmt.Fprintf(w, "needle %d\n", i)
		} else {
			_, _ = fmt.Fprintf(w, "log line %d\n", i)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	tool := &GrepTool{CWD: dir}
	// HeapAlloc counts garbage not yet collected, and at the default GOGC the pacer lets it grow to about the live heap
	// before the next cycle, so the peak would depend on GC timing (a macOS runner measured 21 MB of slack). A low GOGC
	// keeps the peak near what grep retains; loading the ~50 MB file still exceeds the bound.
	defer debug.SetGCPercent(debug.SetGCPercent(10))
	runtime.GC()
	peak := samplePeakHeap(t)
	result := runFileTool(t, tool, t.Context(), map[string]any{"pattern": "needle", "context": 2})
	grown := peak()

	want := "server.log-999998- log line 999998\nserver.log-999999- log line 999999\nserver.log:1000000: needle 1000000\n" +
		"server.log-1000001- log line 1000001\nserver.log-1000002- log line 1000002\n" +
		"server.log-2499998- log line 2499998\nserver.log-2499999- log line 2499999\nserver.log:2500000: needle 2500000\n" +
		"server.log-2500001- log line 2500001\nserver.log-2500002- log line 2500002"
	if result.IsError || result.Text() != want {
		t.Fatalf("result = %+v", result)
	}
	if grown > uint64(info.Size())/4 {
		t.Fatalf("grep with context grew the heap by %d bytes for a %d byte file; it must not load the file", grown, info.Size())
	}
}

// samplePeakHeap samples HeapAlloc until the returned function is called, and
// that function returns the highest sample above the heap at the start.
func samplePeakHeap(t *testing.T) func() uint64 {
	t.Helper()
	var start runtime.MemStats
	runtime.ReadMemStats(&start)
	stop, done := make(chan struct{}), make(chan uint64)
	go func() {
		peak := start.HeapAlloc
		for {
			var m runtime.MemStats
			runtime.ReadMemStats(&m)
			peak = max(peak, m.HeapAlloc)
			select {
			case <-stop:
				done <- peak - start.HeapAlloc
				return
			default:
			}
		}
	}()
	return func() uint64 {
		close(stop)
		return <-done
	}
}
