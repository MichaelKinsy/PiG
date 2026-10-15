// SPDX-License-Identifier: MIT

//go:build darwin

package profiling

import (
	"os"
	"syscall"
)

// PeakRSSBytes is the process's peak resident set in bytes, or -1. Darwin's maxRSS starts afresh at exec.
func PeakRSSBytes() int64 {
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		return -1
	}
	return usage.Maxrss // Darwin reports bytes.
}

// openFDs counts /dev/fd entries; the directory read's own descriptor is excluded.
func openFDs() int64 {
	entries, err := os.ReadDir("/dev/fd")
	if err != nil {
		return -1
	}
	return int64(len(entries)) - 1
}

func threads() int64        { return -1 }
func childProcesses() int64 { return -1 }
