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

func gcPercent() uint64 {
	sample := []metrics.Sample{{Name: "/gc/gogc:percent"}}
	metrics.Read(sample)
	return sample[0].Value.Uint64()
}

func unsetGOGC(t *testing.T) {
	t.Setenv("GOGC", "")
	if err := os.Unsetenv("GOGC"); err != nil {
		t.Fatal(err)
	}
}

// D175: with GOGC unset the target holds over collections of a small heap, and the first collection that finds more than
// FallbackLiveBytes live restores the default for the rest of the process.
func TestApplyFallsBackAboveTheLiveHeapLimit(t *testing.T) {
	unsetGOGC(t)
	defer debug.SetGCPercent(debug.SetGCPercent(DefaultPercent))
	Apply()
	for range 3 {
		runtime.GC()
		time.Sleep(10 * time.Millisecond) // Room for the cleanup of the collection to run.
	}
	if got := gcPercent(); got != TargetPercent {
		t.Fatalf("GC percent %d after collections of a small heap, want %d", got, TargetPercent)
	}
	live := make([]byte, FallbackLiveBytes+16<<20)
	deadline := time.Now().Add(10 * time.Second)
	for gcPercent() != DefaultPercent {
		if time.Now().After(deadline) {
			t.Fatalf("GC percent %d with %d bytes live, want %d", gcPercent(), len(live), DefaultPercent)
		}
		runtime.GC()
		time.Sleep(time.Millisecond)
	}
	runtime.KeepAlive(live)
}

// D175: a GOGC in the environment wins; the policy leaves the GC percent alone.
func TestApplyLeavesAUserGOGC(t *testing.T) {
	t.Setenv("GOGC", "50")
	defer debug.SetGCPercent(debug.SetGCPercent(50))
	Apply()
	if got := gcPercent(); got != 50 {
		t.Fatalf("GC percent %d with GOGC=50 set, want 50", got)
	}
}
