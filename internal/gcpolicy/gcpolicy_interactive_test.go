// SPDX-License-Identifier: MIT

package gcpolicy

import (
	"os"
	"runtime"
	"runtime/debug"
	"testing"
	"time"
)

// unsetMemoryLimit removes GOMEMLIMIT from the environment for the test: Setenv to "" would leave it set, which LookupEnv reports.
func unsetMemoryLimit(t *testing.T) {
	t.Setenv("GOMEMLIMIT", "")
	if err := os.Unsetenv("GOMEMLIMIT"); err != nil {
		t.Fatal(err)
	}
}

func waitForPercent(t *testing.T, want uint64, within time.Duration, step func()) {
	t.Helper()
	deadline := time.Now().Add(within)
	for gcPercent() != want {
		if time.Now().After(deadline) {
			t.Fatalf("GC percent %d, want %d", gcPercent(), want)
		}
		if step != nil {
			step()
		}
		time.Sleep(time.Millisecond)
	}
}

// D621: an interactive run collects at InteractivePercent while its live heap is small, and returns to DefaultPercent once a collection
// finds more than InteractiveFallbackLiveBytes live. Print, json and rpc runs keep D175's 400 and 64 MiB: a heap between the two limits
// does not end the batch policy.
func TestInteractiveRevertsAtItsLiveHeapThreshold(t *testing.T) {
	unsetGOGC(t)
	unsetMemoryLimit(t)
	defer debug.SetGCPercent(debug.SetGCPercent(DefaultPercent))
	defer Interactive.Apply()()
	if got := gcPercent(); got != InteractivePercent {
		t.Fatalf("GC percent %d after Apply, want %d", got, InteractivePercent)
	}
	for range 3 {
		runtime.GC()
		time.Sleep(10 * time.Millisecond) // Room for the watcher to read after the collection.
	}
	if got := gcPercent(); got != InteractivePercent {
		t.Fatalf("GC percent %d after collections of a small heap, want %d", got, InteractivePercent)
	}
	live := make([]byte, InteractiveFallbackLiveBytes+4<<20)
	waitForPercent(t, DefaultPercent, 10*time.Second, runtime.GC)
	runtime.KeepAlive(live)
}

func TestBatchKeepsItsTargetBetweenTheInteractiveAndBatchThresholds(t *testing.T) {
	unsetGOGC(t)
	defer debug.SetGCPercent(debug.SetGCPercent(DefaultPercent))
	defer Apply()()
	live := make([]byte, InteractiveFallbackLiveBytes+4<<20)
	for range 3 {
		runtime.GC()
		time.Sleep(10 * time.Millisecond)
	}
	if got := gcPercent(); got != TargetPercent {
		t.Fatalf("GC percent %d with %d bytes live, want the batch target %d", got, len(live), TargetPercent)
	}
}

// D621: the interactive watcher ends at DefaultPercent once the run has gone Idle without a collection, so a process left at its prompt
// reads no more metrics. A policy without Idle keeps its target.
func TestInteractiveStopsWhenIdle(t *testing.T) {
	unsetGOGC(t)
	unsetMemoryLimit(t)
	defer debug.SetGCPercent(debug.SetGCPercent(DefaultPercent))
	idle := Interactive
	idle.Idle = 500 * time.Millisecond
	stop := idle.Apply()
	defer stop()
	if got := gcPercent(); got != InteractivePercent {
		t.Fatalf("GC percent %d after Apply, want %d", got, InteractivePercent)
	}
	waitForPercent(t, DefaultPercent, 5*time.Second, nil)
	deadline := time.Now().Add(5 * time.Second)
	for watcherRunning() {
		if time.Now().After(deadline) {
			t.Fatal("the watcher is still running after the idle limit")
		}
		time.Sleep(time.Millisecond)
	}
	// The next collection takes the target up again with a new watcher (a cleanup of the idle stop runs after it).
	waitForPercent(t, InteractivePercent, 10*time.Second, runtime.GC)
	deadline = time.Now().Add(5 * time.Second)
	for !watcherRunning() {
		if time.Now().After(deadline) {
			t.Fatal("no watcher after the collection that followed the idle stop")
		}
		time.Sleep(time.Millisecond)
	}
	stop()
	if watcherRunning() {
		t.Fatal("the watcher is still running after stop returned")
	}
	for range 3 { // A stopped policy takes no target up again.
		runtime.GC()
		time.Sleep(5 * time.Millisecond)
	}
	if watcherRunning() {
		t.Fatal("a stopped policy started a watcher at a later collection")
	}
	debug.SetGCPercent(DefaultPercent)

	// A collection restarts the clock: a run that keeps collecting is not idle.
	busy := Interactive
	busy.Idle = 300 * time.Millisecond
	defer busy.Apply()()
	end := time.Now().Add(900 * time.Millisecond)
	for time.Now().Before(end) {
		runtime.GC()
		time.Sleep(20 * time.Millisecond)
	}
	if got := gcPercent(); got != InteractivePercent {
		t.Fatalf("GC percent %d in a run that collects every 20 ms with a 300 ms idle limit, want %d", got, InteractivePercent)
	}
}

// D621: a GOGC or GOMEMLIMIT in the environment is the user's setting and wins over the interactive policy.
func TestInteractiveLeavesAUserGOGCOrMemoryLimit(t *testing.T) {
	for _, env := range []string{"GOGC", "GOMEMLIMIT"} {
		t.Run(env, func(t *testing.T) {
			unsetGOGC(t)
			unsetMemoryLimit(t)
			t.Setenv(env, map[string]string{"GOGC": "50", "GOMEMLIMIT": "4GiB"}[env])
			defer debug.SetGCPercent(debug.SetGCPercent(50))
			stop := Interactive.Apply()
			defer stop()
			if got := gcPercent(); got != 50 {
				t.Fatalf("GC percent %d with %s set, want 50", got, env)
			}
			if watcherRunning() {
				t.Fatalf("a watcher is running with %s set", env)
			}
		})
	}
}

// D175 keeps its rule: the batch policy answers only to GOGC, so a GOMEMLIMIT leaves a print run at 400.
func TestBatchIgnoresAMemoryLimit(t *testing.T) {
	unsetGOGC(t)
	t.Setenv("GOMEMLIMIT", "4GiB")
	defer debug.SetGCPercent(debug.SetGCPercent(DefaultPercent))
	defer Apply()()
	if got := gcPercent(); got != TargetPercent {
		t.Fatalf("GC percent %d with GOMEMLIMIT set, want %d", got, TargetPercent)
	}
}
