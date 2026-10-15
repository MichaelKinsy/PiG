//go:build unix

package ai

import (
	"syscall"
	"testing"
	"time"
)

// processCPU is the CPU time, user and system, the process has used. Unlike wall time, other processes on a loaded host do not add to it.
func processCPU(tb testing.TB) time.Duration {
	tb.Helper()
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		tb.Fatal(err)
	}
	return time.Duration(usage.Utime.Nano() + usage.Stime.Nano())
}
