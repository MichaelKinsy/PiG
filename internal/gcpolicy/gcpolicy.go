// SPDX-License-Identifier: MIT

// Package gcpolicy is PiG's GC target for print, json and rpc runs. pig additive (D175): a print, json or rpc run whose environment does not set GOGC
// collects at a GC percent of TargetPercent, so a short run's allocations finish before most of the collections the default target would
// start, and goes back to DefaultPercent once a collection finds more than FallbackLiveBytes live, so a run over a large session keeps the
// default's memory bound.
package gcpolicy

import (
	"os"
	"runtime"
	"runtime/debug"
	"runtime/metrics"
)

const (
	// TargetPercent is the GC percent of a print, json or rpc run.
	TargetPercent = 400
	// DefaultPercent is the Go runtime's default GC percent, which the run returns to for the rest of the process.
	DefaultPercent = 100
	// FallbackLiveBytes is the live heap above which a run returns to DefaultPercent.
	FallbackLiveBytes = 64 << 20
)

// Apply sets the GC target unless the environment sets GOGC, which debug.SetGCPercent would otherwise override. It then
// checks the live heap after every collection until a check finds more than FallbackLiveBytes, which restores DefaultPercent.
func Apply() {
	if _, set := os.LookupEnv("GOGC"); set {
		return
	}
	debug.SetGCPercent(TargetPercent)
	armLiveHeapCheck()
}

// sentinel is unreachable as soon as it is armed, so its cleanup runs once per collection. Sixteen bytes keep it out of the tiny allocator,
// which can keep a small object alive with its neighbours.
type sentinel struct{ _ [16]byte }

func armLiveHeapCheck() {
	runtime.AddCleanup(new(sentinel), checkLiveHeap, struct{}{})
}

// checkLiveHeap reads the live heap the last collection marked.
func checkLiveHeap(struct{}) {
	sample := []metrics.Sample{{Name: "/gc/heap/live:bytes"}}
	metrics.Read(sample)
	if sample[0].Value.Uint64() > FallbackLiveBytes {
		debug.SetGCPercent(DefaultPercent)
		return
	}
	armLiveHeapCheck()
}
