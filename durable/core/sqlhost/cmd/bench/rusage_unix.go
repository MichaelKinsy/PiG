// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

//go:build unix

package main

import (
	"runtime"
	"syscall"
	"time"

	"github.com/MichaelKinsy/PiG/internal/profiling"
)

// haveRusage reports whether processCPU and peakResidentMB measure this process.
const haveRusage = true

// processCPU is the process's CPU time so far, user plus system over every thread, in milliseconds (bench.mjs
// process.cpuUsage()).
func processCPU() float64 {
	var u syscall.Rusage
	if syscall.Getrusage(syscall.RUSAGE_SELF, &u) != nil {
		return 0
	}
	return float64(u.Utime.Nano()+u.Stime.Nano()) / float64(time.Millisecond)
}

// peakResidentMB is the process's peak resident set in MiB: profiling.PeakRSSBytes (VmHWM on Linux, as bench.mjs reads,
// and getrusage on Darwin), else getrusage's maxRSS, which Darwin and iOS report in bytes and the others in KiB.
func peakResidentMB() float64 {
	if b := profiling.PeakRSSBytes(); b >= 0 {
		return float64(b) / (1 << 20)
	}
	var u syscall.Rusage
	if syscall.Getrusage(syscall.RUSAGE_SELF, &u) != nil {
		return 0
	}
	kib := float64(u.Maxrss)
	if runtime.GOOS == "darwin" || runtime.GOOS == "ios" {
		kib /= 1024
	}
	return kib / 1024
}
