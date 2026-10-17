//go:build windows

package ai

import (
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// processCPU is the CPU time, user and system, the process has used. Unlike wall time, other processes on a loaded host do not add to it.
func processCPU(tb testing.TB) time.Duration {
	tb.Helper()
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(windows.CurrentProcess(), &creation, &exit, &kernel, &user); err != nil {
		tb.Fatal(err)
	}
	// A FILETIME counts 100-nanosecond intervals.
	ticks := func(t windows.Filetime) int64 { return int64(t.HighDateTime)<<32 | int64(t.LowDateTime) }
	return time.Duration((ticks(kernel) + ticks(user)) * 100)
}
