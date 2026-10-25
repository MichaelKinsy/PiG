//go:build race && linux

package ai

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

func residentMiB(t *testing.T) int {
	t.Helper()
	status, err := os.ReadFile("/proc/self/status")
	if err != nil {
		t.Fatal(err)
	}
	for line := range strings.SplitSeq(string(status), "\n") {
		if strings.HasPrefix(line, "VmRSS:") {
			var kib int
			if _, err := fmt.Sscanf(line, "VmRSS: %d", &kib); err != nil {
				t.Fatal(err)
			}
			return kib / 1024
		}
	}
	t.Fatal("no VmRSS in /proc/self/status")
	return 0
}

// Under the race detector iter.Pull keeps ~5 KB (a trivial sequence) to ~50 KB (a stream's coroutines) per coroutine for the life of the process, because the runtime ends a coroutine's
// goroutine without racegoend (see pull_race.go). 60,000 pulls of a trivial sequence grew the process by 311 MiB through iter.Pull; the race build's pull must stay flat.
func TestPullDoesNotAccumulateRaceDetectorState(t *testing.T) {
	const pulls = 60_000
	run := func() {
		for range pulls {
			next, stop := pull(func(yield func(int) bool) { yield(1) })
			next()
			stop()
		}
	}
	run() // reach the steady state of the runtime's pools before measuring
	before := residentMiB(t)
	run()
	if grown := residentMiB(t) - before; grown > 48 {
		t.Fatalf("%d pulls grew the process by %d MiB, want the race detector's per-goroutine state released when each sequence ends", pulls, grown)
	}
}
