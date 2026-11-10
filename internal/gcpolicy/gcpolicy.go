// SPDX-License-Identifier: MIT

// Package gcpolicy is PiG's GC target for print, json and rpc runs. pig additive (D175): a print, json or rpc run whose environment does not set GOGC
// collects at a GC percent of TargetPercent, so a short run's allocations finish before most of the collections the default target would
// start, and goes back to DefaultPercent once a collection finds more than FallbackLiveBytes live, so a run over a large session keeps the
// default's memory bound.
package gcpolicy

import (
	"os"
	"runtime/debug"
	"runtime/metrics"
	"time"
)

const (
	// TargetPercent is the GC percent of a print, json or rpc run.
	TargetPercent = 400
	// DefaultPercent is the Go runtime's default GC percent, which the run returns to for the rest of the process.
	DefaultPercent = 100
	// FallbackLiveBytes is the live heap above which a run returns to DefaultPercent.
	FallbackLiveBytes = 64 << 20
	// minPoll and maxPoll bound the wait between reads of the live heap.
	minPoll = 5 * time.Millisecond
	maxPoll = 100 * time.Millisecond
)

// Apply sets the GC target unless the environment sets GOGC, which debug.SetGCPercent would otherwise override. It then
// watches the live heap until a read finds more than FallbackLiveBytes, which restores DefaultPercent.
func Apply() {
	if _, set := os.LookupEnv("GOGC"); set {
		return
	}
	debug.SetGCPercent(TargetPercent)
	go watchLiveHeap()
}

// watchLiveHeap polls the runtime's metrics until one read finds more than FallbackLiveBytes live. It polls rather than waiting for a
// cleanup after each collection because a cleanup runs when the sweeper reaches the sentinel's span (late under load, with a 400 target
// spreading the sweep over a long runway) and a sentinel allocated while a collection is marking survives that collection. Either
// one could skip the collection that found the large heap, and the target would then stay at 400 for the rest of the run. The wait
// doubles from minPoll to maxPoll while no collection has finished, so an idle server wakes ten times a second at most.
func watchLiveHeap() {
	sample := []metrics.Sample{{Name: "/gc/heap/live:bytes"}, {Name: "/gc/cycles/total:gc-cycles"}}
	var cycles uint64
	wait := minPoll
	for {
		time.Sleep(wait)
		metrics.Read(sample)
		if sample[0].Value.Uint64() > FallbackLiveBytes {
			debug.SetGCPercent(DefaultPercent)
			return
		}
		if now := sample[1].Value.Uint64(); now != cycles {
			cycles, wait = now, minPoll
		} else {
			wait = min(2*wait, maxPoll)
		}
	}
}
