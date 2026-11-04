//go:build race && linux

package ai

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
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
// goroutine without racegoend (see pull_race.go). 60,000 pulls of a trivial sequence grew the process by 311 MiB through iter.Pull; the race build's pull must stay under 48 MiB. It cannot stay flat: the
// race runtime keeps ~0.3 KB for every goroutine that ends, a bare `go func() {}()` included, and a pull is one goroutine (31-39 MiB for these 60,000, measured over 120 fresh processes).
//
// The test runs in a process of its own (testenv.InOwnProcess), because the resident memory of the package's process moves with what the earlier tests left, not only with the pulls. Memory they freed
// leaves the resident set in the background and later allocation brings pages back, the race detector's shadow of a freed page stays resident for a while, and the collector's heap goal decides how many fresh
// pages the pulls' garbage reaches. The same 60,000 pulls changed the package's process by -47, +46 and +20 MiB in one instrumented `-count=3` run, and by 59-85 MiB, over the bound, in 8 of 10 others.
func TestPullDoesNotAccumulateRaceDetectorState(t *testing.T) {
	if !testenv.InOwnProcess(t) {
		return
	}
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
