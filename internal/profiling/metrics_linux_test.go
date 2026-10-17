// SPDX-License-Identifier: MIT

//go:build linux

package profiling

import (
	"os/exec"
	"runtime"
	"testing"
)

// An exited child that was never waited for is a zombie; the count must see it until Wait reaps it.
func TestCollectMetricsCountsUnwaitedChildren(t *testing.T) {
	before := CollectMetrics().ChildProcesses
	child := exec.Command("true")
	if err := child.Start(); err != nil {
		t.Skipf("cannot start true: %v", err)
	}
	running := CollectMetrics().ChildProcesses
	if err := child.Wait(); err != nil {
		t.Fatal(err)
	}
	reaped := CollectMetrics().ChildProcesses
	if running != before+1 || reaped != before {
		t.Fatalf("children before/started/reaped = %d/%d/%d", before, running, reaped)
	}
}

// The peak is never below the RSS the process holds now.
func TestCollectMetricsPeakRSSCoversCurrentRSS(t *testing.T) {
	retained := make([]byte, 64<<20)
	for i := range retained {
		retained[i] = 1
	}
	current := statusField("VmRSS:") * 1024
	record := CollectMetrics()
	if current <= 0 || record.MaxRSSBytes < current {
		t.Fatalf("peak RSS %d is below the current RSS %d", record.MaxRSSBytes, current)
	}
	runtime.KeepAlive(retained)
}
