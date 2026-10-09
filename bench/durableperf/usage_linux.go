//go:build linux

package main

import (
	"os"
	"strconv"
	"strings"
	"syscall"
)

func rssMB() (current, peak float64) {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0, 0
	}
	//portlint:allow pathseparators /proc/self/status uses LF line ends
	for line := range strings.SplitSeq(string(data), "\n") {
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		fields := strings.Fields(value)
		if len(fields) == 0 {
			continue
		}
		kb, _ := strconv.ParseFloat(fields[0], 64)
		switch name {
		case "VmRSS":
			current = kb / 1024
		case "VmHWM":
			peak = kb / 1024
		}
	}
	return current, peak
}

func cpuSeconds() float64 {
	var usage syscall.Rusage
	if syscall.Getrusage(syscall.RUSAGE_SELF, &usage) != nil {
		return 0
	}
	return float64(usage.Utime.Sec+usage.Stime.Sec) + float64(usage.Utime.Usec+usage.Stime.Usec)/1e6
}

// switches are the process's context switches: voluntary ones are blocking waits, such as a futex sleep.
func switches() (voluntary, involuntary int64) {
	var usage syscall.Rusage
	if syscall.Getrusage(syscall.RUSAGE_SELF, &usage) != nil {
		return 0, 0
	}
	return usage.Nvcsw, usage.Nivcsw
}
