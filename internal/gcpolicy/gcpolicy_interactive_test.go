// SPDX-License-Identifier: MIT

package gcpolicy

import (
	"os"
	"runtime"
	"runtime/debug"
	"runtime/metrics"
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
	defer interactiveWithoutFloor().Apply()()
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
	idle := interactiveWithoutFloor()
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
	busy := interactiveWithoutFloor()
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

// interactiveWithoutFloor is the interactive policy at its fixed percent, for the tests of the live-heap threshold and the idle stop.
func interactiveWithoutFloor() Policy {
	p := Interactive
	p.GoalFloor = 0
	return p
}

// D621: below the goal floor the percent after a collection puts the next heap goal at the floor, never under the policy's percent and
// never over the percent whose minimum heap (4 MiB at 100) is the floor.
func TestGoalFloorPercent(t *testing.T) {
	const MiB = 1 << 20
	p := Policy{Percent: 150, GoalFloor: 16 * MiB}
	for _, tc := range []struct {
		live, scan uint64
		want       int
	}{
		{0, 0, 400},             // nothing collected yet: the runtime's minimum heap, 4 MiB x 400 %, is the floor
		{MiB / 2, MiB / 2, 400}, // a tiny heap: the ceiling
		{3 * MiB, MiB, 325},     // 3 + (3+1) x 3.25 = 16
		{5 * MiB, MiB, 183},     // 5 + (5+1) x 1.83 = 16
		{7 * MiB, MiB, 150},     // the policy's percent already reaches the floor
		{16 * MiB, MiB, 150},    // at the floor
		{40 * MiB, MiB, 150},    // above it
	} {
		if got := p.percentFor(p.Percent, tc.live, tc.scan); got != tc.want {
			t.Errorf("percentFor(%d MiB live, %d MiB scan) = %d, want %d", tc.live/MiB, tc.scan/MiB, got, tc.want)
		}
	}
	if got := p.percentFor(DefaultPercent, 7*MiB, MiB); got != 112 {
		t.Errorf("past the fallback, 7 MiB live answers %d, want 112: 7 + (7+1) x 1.12 = 16", got)
	}
	if got := (Policy{Percent: 150}).percentFor(150, 3*MiB, MiB); got != 150 {
		t.Errorf("a policy without a floor answers %d, want its percent", got)
	}
}

// D621: an interactive run starts at the floor's percent, keeps a percent between the policy's and the floor's while its live heap is small,
// and still returns to DefaultPercent past InteractiveFallbackLiveBytes.
func TestInteractiveKeepsItsGoalFloorWhileTheHeapIsSmall(t *testing.T) {
	unsetGOGC(t)
	unsetMemoryLimit(t)
	defer debug.SetGCPercent(debug.SetGCPercent(DefaultPercent))
	defer Interactive.Apply()()
	ceiling := uint64(InteractiveGoalFloorBytes * 100 / minHeap)
	if got := gcPercent(); got != ceiling {
		t.Fatalf("GC percent %d after Apply, want %d", got, ceiling)
	}
	for range 3 {
		runtime.GC()
		time.Sleep(10 * time.Millisecond) // Room for the watcher to read after the collection.
	}
	if got := gcPercent(); got < InteractivePercent || got > ceiling {
		t.Fatalf("GC percent %d after collections of a small heap, want %d to %d", got, InteractivePercent, ceiling)
	}
	live := make([]byte, InteractiveFallbackLiveBytes+4<<20)
	waitForPercent(t, DefaultPercent, 10*time.Second, runtime.GC)
	runtime.KeepAlive(live)
}

// D621: past InteractiveFallbackLiveBytes the run's percent is DefaultPercent for good, and the floor holds again when the live heap
// shrinks below it, as it does when a long session drops what it kept.
func TestInteractiveGoalFloorHoldsAgainAfterTheHeapShrinks(t *testing.T) {
	unsetGOGC(t)
	unsetMemoryLimit(t)
	defer debug.SetGCPercent(debug.SetGCPercent(DefaultPercent))
	defer Interactive.Apply()()
	live := make([]byte, InteractiveFallbackLiveBytes+12<<20)
	waitForPercent(t, DefaultPercent, 10*time.Second, runtime.GC)
	runtime.KeepAlive(live)
	live = nil
	deadline := time.Now().Add(10 * time.Second)
	for gcPercent() <= DefaultPercent {
		if time.Now().After(deadline) {
			t.Fatalf("GC percent %d after the live heap shrank below the floor, want it above %d", gcPercent(), DefaultPercent)
		}
		runtime.GC()
		time.Sleep(10 * time.Millisecond)
	}
}

// heapGoal reads the runtime's heap goal with the live heap and the scan work it was computed from.
func heapGoal() (goal, live, scan uint64) {
	sample := []metrics.Sample{{Name: "/gc/heap/goal:bytes"}, {Name: "/gc/heap/live:bytes"}, {Name: "/gc/scan/globals:bytes"}, {Name: "/gc/scan/stack:bytes"}}
	metrics.Read(sample)
	return sample[0].Value.Uint64(), sample[1].Value.Uint64(), sample[2].Value.Uint64() + sample[3].Value.Uint64()
}

// D621, the rule as the runtime applies it: after a collection the heap goal of an interactive run is the larger of 16 MiB and the goal
// of the run's percent, which is 150 until a collection has found more than 8 MiB live and 100 from then on, also when the heap has
// shrunk again. Each row holds that much heap live, collects, and compares the runtime's goal with the rule's for the live heap and
// scan work the runtime reports.
func TestInteractiveHeapGoalByLiveHeap(t *testing.T) {
	unsetGOGC(t)
	unsetMemoryLimit(t)
	defer debug.SetGCPercent(debug.SetGCPercent(DefaultPercent))
	defer Interactive.Apply()()
	const (
		MiB   = 1 << 20
		floor = 16 * MiB
	)
	var held []byte
	for _, tc := range []struct {
		hold    int    // MiB the test keeps live, on top of the test binary's own heap
		percent uint64 // the run's percent at this row
		floored bool   // whether the floor, not the percent, sets the goal
	}{
		{0, 150, true},
		{3, 150, true},
		{5, 150, true},
		{7, 150, false},  // 7 + 7 x 1.5 is past the floor
		{12, 100, false}, // more than 8 MiB live: the run's percent is 100 from here on
		{20, 100, false},
		{7, 100, true}, // shrunk again: at 100 the percent's goal for 7 MiB is under the floor
		{3, 100, true},
		{10, 100, false},
	} {
		held = nil
		held = make([]byte, tc.hold*MiB)
		for i := 0; i < len(held); i += 4096 {
			held[i] = 1
		}
		var goal, live, scan, want uint64
		deadline := time.Now().Add(5 * time.Second)
		for {
			runtime.GC()
			time.Sleep(10 * time.Millisecond) // Room for the watcher to read after the collection.
			goal, live, scan = heapGoal()
			want = max(floor, live+(live+scan)*tc.percent/100)
			if diff := int64(goal) - int64(want); -floor/50 <= diff && diff <= floor/50 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("%d MiB held (%d bytes live, %d bytes of scan work): heap goal %d, want %d (the larger of %d and the goal of %d %%)", tc.hold, live, scan, goal, want, uint64(floor), tc.percent)
			}
		}
		if floored := want == floor; floored != tc.floored {
			t.Errorf("%d MiB held (%d bytes live): the floor sets the goal = %v, want %v", tc.hold, live, floored, tc.floored)
		}
	}
	runtime.KeepAlive(held)
}
