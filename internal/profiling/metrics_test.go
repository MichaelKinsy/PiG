// SPDX-License-Identifier: MIT

package profiling

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

var metricsSink []*[64]byte

func TestCollectMetricsCountsAllocationsAndLiveHeap(t *testing.T) {
	before := CollectMetrics()
	const objects = 10_000
	metricsSink = make([]*[64]byte, objects)
	for i := range metricsSink {
		metricsSink[i] = new([64]byte)
	}
	after := CollectMetrics()
	if got := after.AllocObjects - before.AllocObjects; got < objects {
		t.Errorf("allocated %d objects, counted %d", objects, got)
	}
	if got := after.AllocBytes - before.AllocBytes; got < objects*64 {
		t.Errorf("allocated %d bytes, counted %d", objects*64, got)
	}
	if after.HeapLiveBytes < objects*64 {
		t.Errorf("live heap %d bytes holds fewer than the %d retained bytes", after.HeapLiveBytes, objects*64)
	}
	if after.GCCycles <= before.GCCycles {
		t.Errorf("the record's own collection between reads was not counted: %d then %d", before.GCCycles, after.GCCycles)
	}
	if after.Goroutines == 0 || after.GoVersion != runtime.Version() {
		t.Errorf("record = %+v", after)
	}
	metricsSink = nil
}

// The record counts the goroutines started and splits the file-revision stats into auth.json's and models.json's.
func TestCollectMetricsCountsGoroutinesCreatedAndFileStats(t *testing.T) {
	before := CollectMetrics()
	const started = 5
	done := make(chan struct{})
	for range started {
		go func() { done <- struct{}{} }()
	}
	for range started {
		<-done
	}
	FileRevisionStats.Add(3) // Two auth.json stats and one models.json stat.
	ModelsFileStats.Add(1)
	after := CollectMetrics()
	if got := after.GoroutinesCreated - before.GoroutinesCreated; got < started {
		t.Errorf("started %d goroutines, counted %d", started, got)
	}
	if got := after.AuthFileStats - before.AuthFileStats; got != 2 {
		t.Errorf("auth.json stats = %d, want 2", got)
	}
	if got := after.ModelsFileStats - before.ModelsFileStats; got != 1 {
		t.Errorf("models.json stats = %d, want 1", got)
	}
}

// cachedSpanObject is a size class whose 80 KiB span holds 3 objects: 2 of them stay in a cached span and far outweigh what CollectMetrics allocates after its own read.
type cachedSpanObject [27264]byte

var cachedSpanSink []*cachedSpanObject

// The runtime adds a span's allocations to the cumulative counters only when the span leaves its P's cache (a refill or a collection), so a span still cached holds allocations a plain runtime/metrics read has not seen yet.
func TestCollectMetricsCountsAllocationsInCachedSpans(t *testing.T) {
	before := CollectMetrics()
	runtime.GC() // Empties every P's cache, so the objects below start a fresh span.
	const objects = 2
	cachedSpanSink = make([]*cachedSpanObject, objects)
	for i := range cachedSpanSink {
		cachedSpanSink[i] = new(cachedSpanObject)
	}
	after := CollectMetrics()
	if got, want := after.AllocBytes-before.AllocBytes, uint64(objects*len(cachedSpanObject{})); got < want {
		t.Errorf("allocated %d bytes in a cached span, counted %d", want, got)
	}
	cachedSpanSink = nil
}

func TestCollectMetricsCountsDescriptors(t *testing.T) {
	before := CollectMetrics()
	if before.OpenFDs < 0 {
		t.Skipf("%s reports no descriptor count", runtime.GOOS)
	}
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	opened := CollectMetrics()
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	closed := CollectMetrics()
	if opened.OpenFDs != before.OpenFDs+1 || closed.OpenFDs != before.OpenFDs {
		t.Fatalf("descriptors before/open/closed = %d/%d/%d", before.OpenFDs, opened.OpenFDs, closed.OpenFDs)
	}
}

func TestStartWithMetricsWritesTheRecord(t *testing.T) {
	dir := t.TempDir()
	StartWith("metrics", dir, io.Discard)()
	matches, _ := filepath.Glob(filepath.Join(dir, "pig-*-metrics.json"))
	if len(matches) != 1 {
		t.Fatalf("want one metrics file, got %v", matches)
	}
	data, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	var record ProcessMetrics
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatalf("metrics file %s: %v", data, err)
	}
	if record.AllocObjects == 0 || record.AllocBytes == 0 || record.HeapLiveBytes == 0 || record.Goroutines == 0 {
		t.Fatalf("record = %+v", record)
	}
	if runtime.GOOS == "linux" && (record.MaxRSSBytes <= 0 || record.Threads <= 0 || record.ChildProcesses != 0 || record.OpenFDsAtStart != record.OpenFDs) {
		t.Fatalf("linux record = %+v", record)
	}
}
