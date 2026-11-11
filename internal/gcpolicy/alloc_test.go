// SPDX-License-Identifier: MIT

package gcpolicy

import (
	"os"
	"os/exec"
	"runtime"
	"runtime/debug"
	"testing"
	"time"
)

// allocChildEnv marks the process TestApplyAllocatesLittleBeforeTheFirstCollection starts to measure in.
const allocChildEnv = "PIG_GCPOLICY_ALLOC_CHILD"

// D175: a run that finishes before its first collection pays for the watcher's goroutine, its timer and its two channels and nothing
// else. The first read of runtime/metrics builds the runtime's metric table (12,704 bytes in 72 objects for Apply and a watcher that
// polled for 60 ms, 672 bytes in 11 with the table already built), and a run whose heap never reaches a collection has no live heap to
// read, so the watcher waits for a collection before it reads metrics. The table is built once per process, so the measurement runs in
// a process of its own that has read no metric.
func TestApplyAllocatesLittleBeforeTheFirstCollection(t *testing.T) {
	if os.Getenv(allocChildEnv) == "" {
		command := exec.Command(os.Args[0], "-test.run=^TestApplyAllocatesLittleBeforeTheFirstCollection$", "-test.count=1", "-test.v")
		command.Env = append(os.Environ(), allocChildEnv+"=1")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("%v\n%s", err, output)
		}
		return
	}
	unsetGOGC(t)
	defer debug.SetGCPercent(debug.SetGCPercent(DefaultPercent))
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	stop := Apply()
	time.Sleep(10 * minPoll) // Several polls: the waits double from minPoll.
	stop()
	runtime.ReadMemStats(&after)
	if after.NumGC != before.NumGC {
		t.Fatalf("%d collections ran during the measurement", after.NumGC-before.NumGC)
	}
	bytes, objects := after.TotalAlloc-before.TotalAlloc, after.Mallocs-before.Mallocs
	t.Logf("Apply, a watcher that polled for %v and stop allocated %d bytes in %d objects", 10*minPoll, bytes, objects)
	const maxBytes, maxObjects = 2048, 24
	if bytes > maxBytes || objects > maxObjects {
		t.Fatalf("Apply and a watcher that saw no collection allocated %d bytes in %d objects, want at most %d in %d", bytes, objects, maxBytes, maxObjects)
	}
}
