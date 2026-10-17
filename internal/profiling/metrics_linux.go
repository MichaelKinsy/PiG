// SPDX-License-Identifier: MIT

//go:build linux

package profiling

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

// PeakRSSBytes is the process's peak resident set in bytes, or -1. It reads VmHWM from /proc/self/status, the high-water mark of the process's own address space. getrusage's ru_maxrss reads the same high-water counter without folding in the current RSS, which the kernel syncs into it only at intervals, so a running process can see a peak below its current RSS; it also carries the peak of the address space the process was exec'd from, which is the parent's when the parent spawned it with vfork (Go's os/exec, Python's subprocess, Node's child_process).
func PeakRSSBytes() int64 {
	kilobytes := statusField("VmHWM:")
	if kilobytes < 0 {
		var usage syscall.Rusage
		if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
			return -1
		}
		return usage.Maxrss * 1024 // Linux reports kilobytes.
	}
	return kilobytes * 1024
}

// statusField returns the first number of a /proc/self/status line, or -1.
func statusField(name string) int64 {
	status, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return -1
	}
	for line := range bytes.SplitSeq(status, []byte("\n")) {
		if value, ok := bytes.CutPrefix(line, []byte(name)); ok {
			fields := bytes.Fields(value)
			if len(fields) == 0 {
				return -1
			}
			n, err := strconv.ParseInt(string(fields[0]), 10, 64)
			if err != nil {
				return -1
			}
			return n
		}
	}
	return -1
}

// openFDs counts /proc/self/fd entries; the directory read's own descriptor is excluded.
func openFDs() int64 {
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return -1
	}
	return int64(len(entries)) - 1
}

func threads() int64 { return statusField("Threads:") }

// childProcesses counts the children of every thread, listed in /proc/self/task/*/children. Zombies are children until they are waited for.
func childProcesses() int64 {
	lists, err := filepath.Glob("/proc/self/task/*/children")
	if err != nil || len(lists) == 0 {
		return -1
	}
	var count int64
	for _, list := range lists {
		data, err := os.ReadFile(list)
		if err != nil {
			continue // The thread exited between the glob and the read.
		}
		count += int64(len(bytes.Fields(data)))
	}
	return count
}
