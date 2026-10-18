// SPDX-License-Identifier: MIT

package profiling

import (
	"encoding/json"
	"io"
	"math"
	"runtime"
	"runtime/metrics"
	"sync"
)

// ProcessMetrics is the PIG_PROFILE=metrics record: whole-process resource counts taken when pig exits, after it has stopped its extensions and model services.
//
// The allocation and GC-cycle counters cover the whole process lifetime. Allocation counts do not depend on machine speed, so count gates compare them with ceilings; RSS, the pause total and the counts of goroutines, descriptors and children describe the exit state. A field that the platform cannot report is -1.
type ProcessMetrics struct {
	GoVersion string `json:"go_version"`
	GOOS      string `json:"goos"`
	GOARCH    string `json:"goarch"`
	// AllocObjects and AllocBytes are the cumulative heap allocations (/gc/heap/allocs:objects and :bytes).
	AllocObjects uint64 `json:"alloc_objects"`
	AllocBytes   uint64 `json:"alloc_bytes"`
	// GCCycles counts completed GC cycles before the record's own collection.
	GCCycles uint64 `json:"gc_cycles"`
	// GCPercent is the GC percent at exit (/gc/gogc:percent), and HeapGoalBytes the heap size the last completed cycle set as the next goal (/gc/heap/goal:bytes), both read before the record's own collection: a policy that changes the percent (D175) shows here.
	GCPercent     uint64 `json:"gc_percent"`
	HeapGoalBytes uint64 `json:"heap_goal_bytes"`
	// GOMAXPROCS is the width when the record is read: the D174 cap, or the runtime default while a parallel section is open.
	GOMAXPROCS int `json:"gomaxprocs"`
	// GCPauses counts stop-the-world GC pauses; GCPauseSeconds approximates their total from the runtime's histogram buckets. It is a time, so gates do not compare it.
	GCPauses       uint64  `json:"gc_pauses"`
	GCPauseSeconds float64 `json:"gc_pause_seconds"`
	// HeapLiveBytes is the live heap after the forced collections at exit (collectRetained), so it excludes sync.Pool contents.
	HeapLiveBytes uint64 `json:"heap_live_bytes"`
	// MappedBytes is all memory the Go runtime has mapped (/memory/classes/total:bytes).
	MappedBytes uint64 `json:"mapped_bytes"`
	// MaxRSSBytes is the peak resident set size of the process.
	MaxRSSBytes int64 `json:"max_rss_bytes"`
	// Goroutines, OpenFDs, Threads and ChildProcesses are the exit state. A child that has exited but was not waited for still counts.
	Goroutines uint64 `json:"goroutines"` // runtime.NumGoroutine, which excludes the runtime's own goroutines
	OpenFDs    int64  `json:"open_fds"`
	// OpenFDsAtStart is the descriptor count when profiling started, which includes descriptors the parent let the process inherit; OpenFDs minus it is what the process opened and did not close. CollectMetrics leaves it -1.
	OpenFDsAtStart int64 `json:"open_fds_at_start"`
	Threads        int64 `json:"threads"`
	ChildProcesses int64 `json:"child_processes"`
	// GoroutinesCreated counts the goroutines the process started over its lifetime (/sched/goroutines-created:goroutines), the runtime's own included.
	GoroutinesCreated uint64 `json:"goroutines_created"`
	// AuthFileStats and ModelsFileStats count the revision stats of auth.json and of models.json (FileRevisionStats, ModelsFileStats).
	AuthFileStats   uint64 `json:"auth_file_stats"`
	ModelsFileStats uint64 `json:"models_file_stats"`
}

// flushStats receives the ReadMemStats call that flushes the allocation caches; it is package state so the flush itself does not allocate, and flushMu serializes concurrent collections over it.
var (
	flushMu    sync.Mutex
	flushStats runtime.MemStats
)

// CollectMetrics reads the process counters now. It forces two garbage collections (collectRetained) to measure the retained live heap after reading the cumulative counters.
func CollectMetrics() ProcessMetrics {
	samples := []metrics.Sample{
		{Name: "/gc/heap/allocs:objects"},
		{Name: "/gc/heap/allocs:bytes"},
		{Name: "/gc/cycles/total:gc-cycles"},
		{Name: "/sched/pauses/total/gc:seconds"},
		{Name: "/sched/goroutines-created:goroutines"},
		{Name: "/gc/gogc:percent"},
		{Name: "/gc/heap/goal:bytes"},
	}
	// The runtime adds a span's allocations to the cumulative counters only when the span leaves its P's cache, and a runtime/metrics read does not flush the caches, so a plain read misses up to a span per size class: whether a collection happened to flush them shortly before exit moved the count by half a megabyte. ReadMemStats flushes every P's cache without collecting.
	flushMu.Lock()
	runtime.ReadMemStats(&flushStats)
	flushMu.Unlock()
	metrics.Read(samples)
	record := ProcessMetrics{
		GoVersion:    runtime.Version(),
		GOOS:         runtime.GOOS,
		GOARCH:       runtime.GOARCH,
		AllocObjects: uint64Sample(samples[0]),
		AllocBytes:   uint64Sample(samples[1]),
		GCCycles:     uint64Sample(samples[2]),

		GoroutinesCreated: uint64Sample(samples[4]),
		GCPercent:         uint64Sample(samples[5]),
		HeapGoalBytes:     uint64Sample(samples[6]),
		GOMAXPROCS:        runtime.GOMAXPROCS(0),
	}
	models := ModelsFileStats.Load()
	record.ModelsFileStats = models
	record.AuthFileStats = FileRevisionStats.Load() - models
	record.GCPauses, record.GCPauseSeconds = histogramTotal(samples[3])
	collectRetained()
	after := []metrics.Sample{
		{Name: "/gc/heap/live:bytes"},
		{Name: "/memory/classes/total:bytes"},
	}
	metrics.Read(after)
	record.HeapLiveBytes = uint64Sample(after[0])
	record.MappedBytes = uint64Sample(after[1])
	record.Goroutines = uint64(runtime.NumGoroutine()) // User goroutines only; /sched/goroutines also counts the runtime's GC workers.
	record.MaxRSSBytes = PeakRSSBytes()
	record.OpenFDs = openFDs()
	record.OpenFDsAtStart = -1
	record.Threads = threads()
	record.ChildProcesses = childProcesses()
	return record
}

func uint64Sample(sample metrics.Sample) uint64 {
	if sample.Value.Kind() != metrics.KindUint64 {
		return 0
	}
	return sample.Value.Uint64()
}

// histogramTotal returns the number of observations and their approximate sum. Each bucket counts at its finite positive lower bound, else at its finite upper bound.
func histogramTotal(sample metrics.Sample) (count uint64, sum float64) {
	if sample.Value.Kind() != metrics.KindFloat64Histogram {
		return 0, 0
	}
	histogram := sample.Value.Float64Histogram()
	for i, n := range histogram.Counts {
		if n == 0 {
			continue
		}
		count += n
		bound := histogram.Buckets[i]
		if bound <= 0 || math.IsInf(bound, 0) {
			bound = histogram.Buckets[i+1]
		}
		if bound > 0 && !math.IsInf(bound, 0) {
			sum += float64(n) * bound //portlint:allow fusedmuladd PiG's runtime-metrics profile summary: no Pi counterpart
		}
	}
	return count, sum
}

func writeMetrics(w io.Writer, record ProcessMetrics) error {
	//portlint:allow jsonescape PIG_PROFILE=metrics output is Go-only, read by the perf ratchet; no Pi counterpart writes it
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(record)
}
