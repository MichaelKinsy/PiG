// SPDX-License-Identifier: MIT

// Package gcpolicy is PiG's GC target. pig additive (D175): a print, json or rpc run whose environment does not set GOGC collects at a GC
// percent of TargetPercent, so a short run's allocations finish before most of the collections the default target would start, and goes
// back to DefaultPercent once a collection finds more than FallbackLiveBytes live, so a run over a large session keeps the default's
// memory bound. pig additive (D621): an interactive run whose environment sets neither GOGC nor GOMEMLIMIT keeps this rule.
//
//  1. After each collection the next heap goal is the larger of InteractiveGoalFloorBytes and the goal of the run's percent.
//  2. The run's percent is InteractivePercent until a collection finds more than InteractiveFallbackLiveBytes live, and DefaultPercent
//     from then on for the rest of the process.
//  3. So the floor decides while less than about 6 MiB is live, or 7.5 MiB once the percent is DefaultPercent, which a heap is again
//     after it has shrunk; above that the goal is the percent's alone, as it was without the floor.
//  4. A watcher applies the rule. When the run has gone InteractiveIdle without a collection it sets DefaultPercent and stops, so a run
//     left at its prompt fires no timer; the next collection starts the watcher again, and with it the rule.
//  5. A GOGC or GOMEMLIMIT in the environment, or a --listen server (D232), keeps the runtime's own settings: no percent is set and no
//     watcher runs.
package gcpolicy

import (
	"os"
	"runtime"
	"runtime/debug"
	"runtime/metrics"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// TargetPercent is the GC percent of a print, json or rpc run.
	TargetPercent = 400
	// DefaultPercent is the Go runtime's default GC percent, which the run returns to for the rest of the process.
	DefaultPercent = 100
	// FallbackLiveBytes is the live heap above which a print, json or rpc run returns to DefaultPercent.
	FallbackLiveBytes = 64 << 20
	// InteractivePercent is the GC percent of an interactive run until its live heap has passed InteractiveFallbackLiveBytes (D621).
	InteractivePercent = 150
	// InteractiveFallbackLiveBytes is the live heap above which an interactive run's percent is DefaultPercent for the rest of the process
	// (D621): a transcript that large keeps the default's memory bound, where a raised percent would cost megabytes of resident memory.
	InteractiveFallbackLiveBytes = 8 << 20
	// InteractiveGoalFloorBytes is the least heap goal of an interactive run (D621): a session with 3 to 5 MiB live then collects every 11
	// to 13 MiB it allocates instead of every 5 to 8, and one whose percent alone sets a goal above the floor is unchanged. The run's own
	// memory peaks about 3 MiB higher in a short session.
	InteractiveGoalFloorBytes = 16 << 20
	// InteractiveIdle is how long an interactive run goes without a collection before it stops reading the metrics (D621): a process left at
	// its prompt then fires no timer, so the policy costs it no CPU.
	InteractiveIdle = 2 * time.Second
	// minPoll and maxPoll bound the wait between reads of the live heap.
	minPoll = 5 * time.Millisecond
	maxPoll = time.Second
)

// Policy is a GC target and the point at which a run gives it up.
type Policy struct {
	// Percent is the run's GC percent until a read finds more than FallbackLive live.
	Percent int
	// FallbackLive is the live heap above which the run's percent is DefaultPercent for the rest of the process.
	FallbackLive uint64
	// Idle, when above zero, is how long the run goes without a collection before the watcher ends at DefaultPercent. The policy takes
	// its target up again, with a new watcher, at the next collection.
	Idle time.Duration
	// YieldToMemoryLimit leaves the run alone when the environment sets GOMEMLIMIT, which is the user's own memory bound.
	YieldToMemoryLimit bool
	// GoalFloor, when above zero, is the least heap goal of the run: after each collection the watcher sets the percent that puts the
	// next goal at GoalFloor when the run's percent would put it lower. A policy with a floor keeps its watcher past FallbackLive, so
	// the floor decides again when the live heap has shrunk.
	GoalFloor uint64
}

// minHeap is the runtime's minimum heap goal at GOGC 100; the goal is never below minHeap times the percent.
const minHeap = 4 << 20

// percentFor is the GC percent after a collection that left live bytes of heap and scan bytes of stacks and globals, for a run whose
// percent without the floor is base: the runtime's next goal is live + (live+scan) x percent / 100, and the answer is the percent that
// puts it at GoalFloor, never under base and never over the percent whose minimum heap is GoalFloor (400 for 16 MiB), above which the
// runtime's minimum would exceed the floor.
func (p Policy) percentFor(base int, live, scan uint64) int {
	if p.GoalFloor == 0 || live >= p.GoalFloor {
		return base
	}
	ceiling := int(p.GoalFloor * 100 / minHeap)
	if live+scan == 0 {
		return max(base, ceiling)
	}
	return max(base, min(ceiling, int((p.GoalFloor-live)*100/(live+scan))))
}

var (
	// Batch is the policy of a print, json or rpc run (D175).
	Batch = Policy{Percent: TargetPercent, FallbackLive: FallbackLiveBytes}
	// Interactive is the policy of an interactive run (D621).
	Interactive = Policy{Percent: InteractivePercent, FallbackLive: InteractiveFallbackLiveBytes, Idle: InteractiveIdle, YieldToMemoryLimit: true, GoalFloor: InteractiveGoalFloorBytes}
)

// Apply applies the Batch policy.
func Apply() (stop func()) { return Batch.Apply() }

// Apply sets the GC target unless the environment sets GOGC (or GOMEMLIMIT, for a policy that yields to it), which
// debug.SetGCPercent would otherwise override. It then watches the live heap until a read finds more than FallbackLive, which restores
// DefaultPercent for good; a policy with a GoalFloor keeps watching past that read, at DefaultPercent or the floor's percent. A policy
// with Idle also restores DefaultPercent when the run has gone Idle without a collection, and takes the target up again at the next
// one. The watcher is a goroutine the caller owns: the returned stop ends it and waits until it has returned, and is a no-op when no
// watcher runs (the fallback fired, the run is idle, or the environment is set). stop leaves the GC percent as it is and may be called
// more than once and from any goroutine. A process calls it where it drains its other owned work, before the exit record counts
// goroutines.
func (p Policy) Apply() (stop func()) {
	if _, set := os.LookupEnv("GOGC"); set {
		return func() {}
	}
	if _, set := os.LookupEnv("GOMEMLIMIT"); set && p.YieldToMemoryLimit {
		return func() {}
	}
	percent := p.percentFor(p.Percent, 0, 0)
	debug.SetGCPercent(percent)
	w := &watcher{policy: p}
	w.mu.Lock()
	w.start(percent)
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
	// fellBack is set once a read found more than FallbackLive live under a policy with a GoalFloor: the run's percent is DefaultPercent
	// from then on, and the floor still decides when the live heap has shrunk again.
	fellBack atomic.Bool
}

// basePercent is the run's percent without the floor.
func (w *watcher) basePercent() int {
	if w.fellBack.Load() {
		return DefaultPercent
	}
	return w.policy.Percent
}

// start runs a watchLiveHeap goroutine in a run whose GC percent is percent. w.mu is held.
func (w *watcher) start(percent int) {
	quit := make(chan struct{})
	w.quit = quit
	w.running.Go(func() {
		if w.policy.watchLiveHeap(quit, &w.fellBack, percent) == endedIdle {
			w.resumeAtNextCollection()
		}
	})
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
		percent := w.basePercent()
		debug.SetGCPercent(percent)
		w.start(percent)
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

// watchLiveHeap polls the runtime's metrics until one read finds more than FallbackLive live under a policy without a GoalFloor, or
// Idle has passed without a collection. Every read of a policy with a GoalFloor sets the percent percentFor answers for the heap the
// last collection left, when it differs from percent, the one the run has.
// The live heap changes only when a collection ends, and the runtime has no hook for that moment that does not run late: a cleanup or finalizer runs when the sweeper
// reaches its span and then waits its turn on the goroutines that run them (at most max(GOMAXPROCS/4, 1): one under D174's cap of 2 Ps), and a sentinel allocated while a collection marks survives that collection, so
// a check armed that way can miss the collection that found the large heap and leave the target at 400 for the rest of the run.
// debug.SetMemoryLimit is not a substitute: it caps the absolute heap rather than returning to a percent past a live size.
// The first read of runtime/metrics builds the runtime's metric table (12,704 bytes in 72 objects), and a run that ends before its
// first collection has no live heap to read. Until a collection has finished the poll therefore counts collections with
// collectionsFinished (polls at 5, 15, 35, 75 ms and so on, then one a second), and it reads metrics from the first collection on.
// runtime.ReadMemStats is not the counter: it stops the world and flushes every P's allocation cache on each call, and the polls of a
// 70 ms rpc start left the process 0.5 MB more anonymous memory at ready (4.87 MB against 4.28 MB, ceiling 4.5 MB).
// The wait doubles from minPoll to maxPoll while no collection has finished. An idle process then fires one timer a second, which
// costs about 4 more voluntary context switches a second than a process without the watcher (273 against 39-46 in 60 s, 4-CPU
// Linux container); a run that collects often reads every minPoll. An Idle policy ends that cost after Idle, and a restart that a late cleanup
// triggers is harmless, because the new watcher reads the live heap itself.
func (p Policy) watchLiveHeap(quit <-chan struct{}, fellBack *atomic.Bool, percent int) endReason {
	var sample []metrics.Sample
	var cycles uint64
	wait := minPoll
	lastCollection := time.Now()
	timer := time.NewTimer(wait)
	defer timer.Stop()
	for {
		if watcherTurn != nil {
			watcherTurn()
		}
		select {
		case <-quit:
			return endedStopped
		case <-timer.C:
		}
		if sample == nil && (collected.Load() || collectionsFinished() > 0) {
			collected.Store(true)
			sample = []metrics.Sample{{Name: "/gc/heap/live:bytes"}, {Name: "/gc/cycles/total:gc-cycles"}, {Name: "/gc/scan/globals:bytes"}, {Name: "/gc/scan/stack:bytes"}}
		}
		var now uint64 // No collection has finished while sample is nil: the percent Apply set stands and there is no live heap to read.
		if sample != nil {
			metrics.Read(sample)
			live := sample[0].Value.Uint64()
			if live > p.FallbackLive && !fellBack.Load() {
				if p.GoalFloor == 0 {
					debug.SetGCPercent(DefaultPercent)
					return endedFallback
				}
				fellBack.Store(true)
			}
			if p.GoalFloor > 0 {
				base := p.Percent
				if fellBack.Load() {
					base = DefaultPercent
				}
				if next := p.percentFor(base, live, sample[2].Value.Uint64()+sample[3].Value.Uint64()); next != percent {
					debug.SetGCPercent(next)
					percent = next
				}
			}
			now = sample[1].Value.Uint64()
		}
		if now != cycles {
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

// watcherTurn, when a test sets it before Apply, is called each time the watcher is about to wait: first when it has started and made its
// timer, then after each poll has set the timer again. It lets a test wait for those moments as events. It is nil in production.
var watcherTurn func()

// collected is set by the first poll that finds a finished collection. No later watcher of the process counts collections again: the
// one a cleanup starts after an idle stop runs after a collection and reads metrics at once.
var collected atomic.Bool

// debug.ReadGCStats takes the runtime's heap lock and nothing else (no stop-the-world, no mcache flush), but it makes its two history
// buffers (4,120 and 6,144 bytes) when the caller gives it none. A watcher that polls before the first collection hands it these
// two, which live in the binary's zeroed data, so a poll allocates nothing on the heap; the runtime writes only the entries
// for the collections that have run and three trailing words, so a process that has collected nothing touches one page of them.
// gcStatsMu serializes the watchers of two Apply calls that overlap, which only a test makes.
var (
	gcStatsMu       sync.Mutex
	gcStats         debug.GCStats
	gcPauseScratch  [2*pauseHistory + 3]time.Duration
	gcPauseEndSpace [pauseHistory]time.Time
)

// pauseHistory is the number of collections the runtime keeps pause times for (len(runtime.MemStats{}.PauseNs)); debug.ReadGCStats
// allocates its buffers unless they have room for twice this and three words, and for this many end times.
const pauseHistory = 256

// The compiler rejects a runtime whose history length is not pauseHistory.
var _ = [1]struct{}{}[pauseHistory-len(runtime.MemStats{}.PauseNs)]

// collectionsFinished is the number of collections that have finished, as debug.ReadGCStats reports it.
func collectionsFinished() int64 {
	gcStatsMu.Lock()
	defer gcStatsMu.Unlock()
	gcStats.Pause, gcStats.PauseEnd = gcPauseScratch[:0], gcPauseEndSpace[:0]
	debug.ReadGCStats(&gcStats)
	return gcStats.NumGC
}
