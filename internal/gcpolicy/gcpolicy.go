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
	"sync"
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
	maxPoll = time.Second
)

// Apply sets the GC target unless the environment sets GOGC, which debug.SetGCPercent would otherwise override. It then
// watches the live heap until a read finds more than FallbackLiveBytes, which restores DefaultPercent. The watcher is a goroutine
// the caller owns: the returned stop ends it and waits until it has returned, and is a no-op when the watcher already ended (the
// fallback fired, or GOGC is set). stop leaves the GC percent as it is and may be called more than once and from any goroutine.
// A process calls it where it drains its other owned work, before the exit record counts goroutines.
func Apply() (stop func()) {
	if _, set := os.LookupEnv("GOGC"); set {
		return func() {}
	}
	debug.SetGCPercent(TargetPercent)
	quit, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		watchLiveHeap(quit)
	}()
	var once sync.Once
	return func() {
		once.Do(func() { close(quit) })
		<-done
	}
}

// watchLiveHeap polls until one read of the runtime's metrics finds more than FallbackLiveBytes live. The live heap changes only when a
// collection ends, and the runtime has no hook for that moment that does not run late: a cleanup or finalizer runs when the sweeper
// reaches its span and then waits its turn on the goroutines that run them (at most max(GOMAXPROCS/4, 1): one under D174's cap of 2 Ps), and a sentinel allocated while a collection marks survives that collection, so
// a check armed that way can miss the collection that found the large heap and leave the target at 400 for the rest of the run.
// debug.SetMemoryLimit is not a substitute: it caps the absolute heap rather than returning to a percent past a live size.
// The first read of runtime/metrics builds the runtime's metric table (12,704 bytes in 72 objects), and a run that ends before its
// first collection has no live heap to read. Until a collection has finished the poll therefore counts collections with
// runtime.ReadMemStats, which builds nothing and stops the world for a moment per poll (polls at 5, 15, 35, 75 ms and so on, then one a
// second), and it reads metrics from the first collection on.
// The wait doubles from minPoll to maxPoll while no collection has finished. An idle process then fires one timer a second, which
// costs about 4 more voluntary context switches a second than a process without the watcher (273 against 39-46 in 60 s, 4-CPU
// Linux container); a run that collects often reads every minPoll.
func watchLiveHeap(quit <-chan struct{}) {
	var memstats runtime.MemStats
	var sample []metrics.Sample
	var cycles uint64
	wait := minPoll
	timer := time.NewTimer(wait)
	defer timer.Stop()
	for {
		select {
		case <-quit:
			return
		case <-timer.C:
		}
		if sample == nil {
			if runtime.ReadMemStats(&memstats); memstats.NumGC == 0 {
				wait = min(2*wait, maxPoll)
				timer.Reset(wait)
				continue
			}
			sample = []metrics.Sample{{Name: "/gc/heap/live:bytes"}, {Name: "/gc/cycles/total:gc-cycles"}}
		}
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
		timer.Reset(wait)
	}
}
