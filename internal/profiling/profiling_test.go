// SPDX-License-Identifier: MIT

package profiling

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestStartWithoutProfileIsNoop(t *testing.T) {
	t.Setenv("PIG_PROFILE", "")
	dir := t.TempDir()
	t.Chdir(dir)
	Start()()
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("PIG_PROFILE unset wrote %d files", len(entries))
	}
}

func TestStartWithWritesEveryRequestedKind(t *testing.T) {
	dir := t.TempDir()
	var diag bytes.Buffer
	stop := StartWith("cpu,heap,allocs,block,mutex,goroutine,trace,metrics,bogus", dir, &diag)
	sink := 0
	for i := range 200000 {
		sink += len(strings.Repeat("x", i%7))
	}
	stop()
	stop() // A second call must not write again or panic.
	if !strings.Contains(diag.String(), `unknown PIG_PROFILE kind "bogus"`) {
		t.Errorf("unknown kind was not reported: %q", diag.String())
	}
	for _, suffix := range []string{"-cpu.pprof", "-heap.pprof", "-allocs.pprof", "-block.pprof", "-mutex.pprof", "-goroutine.pprof", ".trace", "-metrics.json"} {
		matches, _ := filepath.Glob(filepath.Join(dir, "pig-*"+suffix))
		if len(matches) != 1 {
			t.Errorf("want one %s file, got %v", suffix, matches)
			continue
		}
		if info, err := os.Stat(matches[0]); err != nil || info.Size() == 0 {
			t.Errorf("%s is empty or missing: %v", matches[0], err)
		}
	}
	if got := strings.Count(diag.String(), "pig: wrote "); got != 8 {
		t.Errorf("reported %d written files, want 8: %s", got, diag.String())
	}
	_ = sink
}

func BenchmarkStartDisabled(b *testing.B) {
	b.Setenv("PIG_PROFILE", "")
	for b.Loop() {
		Start()()
	}
}

// flight keeps a window of the trace in memory and writes it only when asked; a stopped or never started recorder
// writes nothing and reports no error.
func TestFlightRecorderWritesItsWindowOnRequest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "window.trace")
	if err := DumpFlight(path); err != nil || FlightActive() {
		t.Fatalf("before start: active %v, err %v", FlightActive(), err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("a recorder that is not running wrote a file")
	}
	var diag bytes.Buffer
	stop := StartWith("flight", dir, &diag)
	if !FlightActive() {
		t.Fatalf("flight kind did not start the recorder: %s", diag.String())
	}
	sink := 0
	for i := range 100000 {
		sink += len(strings.Repeat("x", i%7))
	}
	_ = sink
	if err := DumpFlight(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || !bytes.HasPrefix(data, []byte("go ")) {
		t.Fatalf("window is not an execution trace (%d bytes): %v %.20q", len(data), err, data)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 && os.PathSeparator == '/' {
		t.Errorf("window mode %v, want 0600", info.Mode().Perm())
	}
	stop()
	if FlightActive() {
		t.Fatal("stop left the recorder running")
	}
	if got := strings.Count(diag.String(), "pig: wrote "); got != 0 {
		t.Errorf("flight alone wrote %d files at exit: %s", got, diag.String())
	}
}

// metrics alone writes one record whose cumulative allocation covers what the process allocated.
func TestMetricsKindWritesAllocationTotals(t *testing.T) {
	dir := t.TempDir()
	var diag bytes.Buffer
	stop := StartWith("metrics", dir, &diag)
	sink := make([][]byte, 0, 64)
	for range 64 {
		sink = append(sink, make([]byte, 4096))
	}
	_ = sink
	stop()
	files, _ := filepath.Glob(filepath.Join(dir, "pig-*-metrics.json"))
	if len(files) != 1 {
		t.Fatalf("metrics files = %v (diag %s)", files, diag.String())
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	var record ProcessMetrics
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatalf("metrics file %q: %v", data, err)
	}
	if record.AllocBytes < 64*4096 || record.AllocObjects < 64 || record.MappedBytes == 0 {
		t.Errorf("metrics %+v do not cover the 64 allocations of 4 KiB the test made", record)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("metrics alone wrote %d files", len(entries))
	}
}

var pooledBuffers sync.Pool

// pooledBufferSize is far above any live heap the test binary has of its own, so a measurement that still counts the pooled buffer is unambiguous.
const pooledBufferSize = 64 << 20

//go:noinline
func poolLargeBuffer() {
	buffer := make([]byte, pooledBufferSize)
	pooledBuffers.Put(&buffer)
}

// A buffer returned to a sync.Pool and never taken again is garbage after two collections: the first moves it to the pool's victim cache, which keeps it reachable for one more cycle. The heap profile and heap_live_bytes report retained memory, so neither counts it.
func TestHeapMeasurementsOmitPooledBuffers(t *testing.T) {
	poolLargeBuffer()
	stop := StartWith("heap", t.TempDir(), io.Discard)
	stop()
	// The heap profile written by stop holds the records as of its last collection; stop ran none after writing, so these are the records it wrote.
	records := make([]runtime.MemProfileRecord, 256)
	n, ok := runtime.MemProfile(records, false)
	for !ok {
		records = make([]runtime.MemProfileRecord, n+64)
		n, ok = runtime.MemProfile(records, false)
	}
	pc := reflect.ValueOf(poolLargeBuffer).Pointer()
	for _, record := range records[:n] {
		for _, frame := range record.Stack() {
			if fn := runtime.FuncForPC(frame); fn != nil && fn.Entry() == pc && record.InUseBytes() >= pooledBufferSize {
				t.Fatalf("the heap profile counts the unused pooled buffer as in use: %d bytes", record.InUseBytes())
			}
		}
	}

	poolLargeBuffer()
	if live := CollectMetrics().HeapLiveBytes; live >= pooledBufferSize {
		t.Fatalf("heap_live_bytes = %d counts the unused pooled buffer", live)
	}
}
