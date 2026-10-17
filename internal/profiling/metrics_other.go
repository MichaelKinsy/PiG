// SPDX-License-Identifier: MIT

//go:build !linux && !darwin

package profiling

// PeakRSSBytes is the process's peak resident set in bytes; this platform reports -1.
func PeakRSSBytes() int64 { return -1 }

func openFDs() int64        { return -1 }
func threads() int64        { return -1 }
func childProcesses() int64 { return -1 }
