// SPDX-License-Identifier: MIT

// Package gcpolicy is PiG's GC target. pig additive (D175): a print, json or rpc run whose environment does not set GOGC collects at a GC
// percent of TargetPercent, so a short run's allocations finish before most of the collections the default target would start, and goes
// back to DefaultPercent once a collection finds more than FallbackLiveBytes live, so a run over a large session keeps the default's
// memory bound. pig additive (D621): an interactive run collects at InteractivePercent until more than InteractiveFallbackLiveBytes are
// live; while it goes InteractiveIdle without a collection it stops polling at DefaultPercent and takes the target up again at its next collection.
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
	// FallbackLiveBytes is the live heap above which a print, json or rpc run returns to DefaultPercent.
	FallbackLiveBytes = 64 << 20
	// InteractivePercent is the GC percent of an interactive run while its live heap is small (D621).
	InteractivePercent = 150
	// InteractiveFallbackLiveBytes is the live heap above which an interactive run returns to DefaultPercent for the rest of the process (D621):
	// a transcript that large keeps the default's memory bound, where a raised percent would cost megabytes of resident memory.
	InteractiveFallbackLiveBytes = 8 << 20
	// InteractiveIdle is how long an interactive run goes without a collection before it stops reading the metrics (D621): a process left at
	// its prompt then fires no timer, so the policy costs it no CPU.
	InteractiveIdle = 2 * time.Second
	// minPoll and maxPoll bound the wait between reads of the live heap.
	minPoll = 5 * time.Millisecond
	maxPoll = time.Second
)

// Policy is a GC target and the point at which a run gives it up.
type Policy struct {
	// Percent is the GC percent while the live heap is at most FallbackLive.
	Percent int
	// FallbackLive is the live heap above which the run returns to DefaultPercent for the rest of the process.
	FallbackLive uint64
	// Idle, when above zero, is how long the run goes without a collection before the watcher ends at DefaultPercent. The policy takes
	// its target up again, with a new watcher, at the next collection.
	Idle time.Duration
	// YieldToMemoryLimit leaves the run alone when the environment sets GOMEMLIMIT, which is the user's own memory bound.
	YieldToMemoryLimit bool
}

var (
	// Batch is the policy of a print, json or rpc run (D175).
	Batch = Policy{Percent: TargetPercent, FallbackLive: FallbackLiveBytes}
	// Interactive is the policy of an interactive run (D621).
	Interactive = Policy{Percent: InteractivePercent, FallbackLive: InteractiveFallbackLiveBytes, Idle: InteractiveIdle, YieldToMemoryLimit: true}
)

// Apply applies the Batch policy.
func Apply() (stop func()) { return Batch.Apply() }

// Apply sets the GC target unless the environment sets GOGC (or GOMEMLIMIT, for a policy that yields to it), which
// debug.SetGCPercent would otherwise override. It then watches the live heap until a read finds more than FallbackLive, which restores
// DefaultPercent for good. A policy with Idle also restores DefaultPercent when the run has gone Idle without a collection, and takes the
// target up again at the next one. The watcher is a goroutine the caller owns: the returned stop ends it and waits until it has returned,
// and is a no-op when no watcher runs (the fallback fired, the run is idle, or the environment is set). stop leaves the GC percent as it
// is and may be called more than once and from any goroutine. A process calls it where it drains its other owned work, before the exit
// record counts goroutines.
func (p Policy) Apply() (stop func()) {
	if _, set := os.LookupEnv("GOGC"); set {
		return func() {}
	}
	if _, set := os.LookupEnv("GOMEMLIMIT"); set && p.YieldToMemoryLimit {
		return func() {}
	}
	debug.SetGCPercent(p.Percent)
	w := &watcher{policy: p}
	w.mu.Lock()
	w.start()
	w.mu.Unlock()
	return w.stop
}

// A watcher runs a policy's watchLiveHeap goroutines: one at a time, a new one after each idle stop at the next collection.
type watcher struct {
	policy  Policy
	running sync.WaitGroup
	mu      sync.Mutex
	quit    chan struct{} // Closed by stop to end the running goroutine.
	stopped bool
}

// start runs a watchLiveHeap goroutine. w.mu is held.
func (w *watcher) start() {
	quit := make(chan struct{})
	w.quit = quit
	w.running.Add(1)
	go func() {
		defer w.running.Done()
		if w.policy.watchLiveHeap(quit) == endedIdle {
			w.resumeAtNextCollection()
		}
	}()
}

// sentinel is what a cleanup is attached to: it has a pointer field so the allocator does not batch it with other tiny objects.
type sentinel struct{ _ *int }

// resumeAtNextCollection takes the target up again, with a new watcher, once a collection has run. A cleanup runs after the cycle that
// found its object unreachable, so the first collection after the idle stop is what restarts the policy. It is late by design: while the
// run is idle the GC percent is DefaultPercent, so a late restart costs the policy's benefit, never memory.
func (w *watcher) resumeAtNextCollection() {
	runtime.AddCleanup(new(sentinel), func(w *watcher) {
		w.mu.Lock()
		defer w.mu.Unlock()
		if w.stopped {
			return
		}
		debug.SetGCPercent(w.policy.Percent)
		w.start()
	}, w)
}

func (w *watcher) stop() {
	w.mu.Lock()
	if !w.stopped {
		w.stopped = true
		if w.quit != nil {
			close(w.quit)
		}
	}
	w.mu.Unlock()
	w.running.Wait()
}

type endReason int

const (
	endedStopped endReason = iota
	endedFallback
	endedIdle
)

// watchLiveHeap polls the runtime's metrics until one read finds more than FallbackLive live, or Idle has passed without a collection.
// The live heap changes only when a collection ends, and the runtime has no hook for that moment that does not run late: a cleanup or finalizer runs when the sweeper
// reaches its span and then waits its turn on the goroutines that run them (at most max(GOMAXPROCS/4, 1): one under D174's cap of 2 Ps), and a sentinel allocated while a collection marks survives that collection, so
// a check armed that way can miss the collection that found the large heap and leave the target at 400 for the rest of the run.
// debug.SetMemoryLimit is not a substitute: it caps the absolute heap rather than returning to a percent past a live size.
// The wait doubles from minPoll to maxPoll while no collection has finished. An idle process then fires one timer a second, which
// costs about 4 more voluntary context switches a second than a process without the watcher (273 against 39-46 in 60 s, 4-CPU
// Linux container); a run that collects often reads every minPoll. An Idle policy ends that cost after Idle, and a restart that a late cleanup
// triggers is harmless, because the new watcher reads the live heap itself.
func (p Policy) watchLiveHeap(quit <-chan struct{}) endReason {
	sample := []metrics.Sample{{Name: "/gc/heap/live:bytes"}, {Name: "/gc/cycles/total:gc-cycles"}}
	var cycles uint64
	wait := minPoll
	lastCollection := time.Now()
	timer := time.NewTimer(wait)
	defer timer.Stop()
	for {
		select {
		case <-quit:
			return endedStopped
		case <-timer.C:
		}
		metrics.Read(sample)
		if sample[0].Value.Uint64() > p.FallbackLive {
			debug.SetGCPercent(DefaultPercent)
			return endedFallback
		}
		if now := sample[1].Value.Uint64(); now != cycles {
			cycles, wait, lastCollection = now, minPoll, time.Now()
		} else {
			wait = min(2*wait, maxPoll)
			if p.Idle > 0 && time.Since(lastCollection) >= p.Idle {
				debug.SetGCPercent(DefaultPercent)
				return endedIdle
			}
		}
		timer.Reset(wait)
	}
}
