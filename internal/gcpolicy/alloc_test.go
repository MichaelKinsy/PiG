// SPDX-License-Identifier: MIT

package gcpolicy

import (
	"os"
	"os/exec"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
	"time"
)

// allocChildEnv marks the process TestApplyAllocatesLittleBeforeTheFirstCollection starts to measure in.
const allocChildEnv = "PIG_GCPOLICY_ALLOC_CHILD"

// D175: a run that finishes before its first collection pays for the watcher's goroutine, its timer and its two channels and nothing
// else. The first read of runtime/metrics builds the runtime's metric table (12,704 bytes in 72 objects for Apply and a watcher that
// polled for 60 ms, 672 bytes in 11 with the table already built), and a run whose heap never reaches a collection has no live heap to
// read, so the watcher waits for a collection before it reads metrics. The table is built once per process and a collection must not
// have run, so the measurement runs in a process of its own that has read no metric and has not collected.
//
// That process has one P, because runtime.MemStats counts the whole process and with more Ps the scheduler allocates in the window as
// well: a thread that was looking for work and finds a goroutine wakes another to look in its place (runtime.resetspinning, wakep),
// and when none is idle it starts one, whose m, two goroutines and two profiling stacks are heap objects (5,576 bytes in 7 objects
// under -race, 5,320 in 6 without; go1.27.4, linux/amd64). With 8 Ps 13 to 22 of 4,800 such processes started a thread in the window
// under -race and 1 of 4,800 without, and each of those exceeded the bound with gcpolicy's own allocations unchanged. With one P no P
// is idle while a goroutine runs, so nothing in the window starts a thread (0 of 10,240), and the watcher always parks on the same P.
// Its polls after the first wait then allocate nothing, which the second bound holds exactly; with 8 Ps a poll that wakes on another P
// takes that P's sudogs or grows its timer heap (up to 272 bytes in 4 objects).
func TestApplyAllocatesLittleBeforeTheFirstCollection(t *testing.T) {
	if os.Getenv(allocChildEnv) == "" {
		command := exec.Command(os.Args[0], "-test.run=^TestApplyAllocatesLittleBeforeTheFirstCollection$", "-test.count=1", "-test.v")
		for _, entry := range os.Environ() {
			if !strings.HasPrefix(entry, "GOGC=") && !strings.HasPrefix(entry, "GOMAXPROCS=") {
				command.Env = append(command.Env, entry)
			}
		}
		command.Env = append(command.Env, allocChildEnv+"=1", "GOMAXPROCS=1")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("%v\n%s", err, output)
		}
		return
	}
	if procs := runtime.GOMAXPROCS(0); procs != 1 {
		t.Fatalf("the measuring process has %d Ps, want 1 (GOMAXPROCS=1)", procs)
	}
	defer debug.SetGCPercent(debug.SetGCPercent(DefaultPercent))
	threads, _ := runtime.ThreadCreateProfile(nil)
	var before, waiting, polled, after runtime.MemStats
	runtime.ReadMemStats(&before)
	if before.NumGC != 0 {
		t.Fatalf("%d collections ran before the measurement", before.NumGC)
	}
	stop := Apply()
	time.Sleep(2 * minPoll) // The watcher has made its timer, parked on it and polled once.
	runtime.ReadMemStats(&waiting)
	time.Sleep(8 * minPoll) // More polls: the waits double from minPoll.
	runtime.ReadMemStats(&polled)
	stop()
	runtime.ReadMemStats(&after)
	if after.NumGC != 0 {
		t.Fatalf("%d collections ran during the measurement", after.NumGC)
	}
	if now, _ := runtime.ThreadCreateProfile(nil); now != threads {
		t.Fatalf("the runtime started %d threads during the measurement, whose objects the counters include", now-threads)
	}
	bytes, objects := after.TotalAlloc-before.TotalAlloc, after.Mallocs-before.Mallocs
	pollBytes, pollObjects := polled.TotalAlloc-waiting.TotalAlloc, polled.Mallocs-waiting.Mallocs
	t.Logf("Apply, a watcher that polled for %v and stop allocated %d bytes in %d objects, the polls after the first wait %d bytes in %d objects",
		10*minPoll, bytes, objects, pollBytes, pollObjects)
	const maxBytes, maxObjects = 2048, 24
	if bytes > maxBytes || objects > maxObjects {
		t.Fatalf("Apply and a watcher that saw no collection allocated %d bytes in %d objects, want at most %d in %d", bytes, objects, maxBytes, maxObjects)
	}
	if pollBytes != 0 || pollObjects != 0 {
		t.Fatalf("the watcher's polls after its first wait allocated %d bytes in %d objects, want none", pollBytes, pollObjects)
	}
}
