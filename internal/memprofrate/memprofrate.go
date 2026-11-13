// SPDX-License-Identifier: MIT

// Package memprofrate stops the Go runtime's heap-profile sampling in a process that did not ask for a heap profile. pig additive (D627): the
// runtime samples one allocation in every 512 KiB by default and keeps a stack record, a special on the object and a slot in a hash table for
// each sample. Only PIG_PROFILE=heap or allocs (internal/profiling) and a measurement program that reads pprof.Lookup("heap") use those
// records, so the package's init sets runtime.MemProfileRate to 0 unless one of them is asked for.
//
// The rate cannot be set before the runtime's own first allocations (//go:debug rejects memprofilerate; GODEBUG=memprofilerate=0 would be
// the only earlier way), so each P's allocation cache takes one more sample on its next allocation after the change. That sample allocates the
// runtime's 1.4 MB table (the profiling-buckets memory class still reads about 1.46 MB: address space whose pages stay untouched), and no
// sample follows it. coding/cli imports the package, so cmd/pig and every Piglet Binary (built from cmd/pig) carry it. A Go extension
// subprocess, built on the pinned extension SDK, does not import it and keeps the default rate.
package memprofrate

import (
	"runtime"
	"strings"
	"syscall"
)

// DefaultRate is the runtime's MemProfileRate until something changes it: one sample per 512 KiB allocated.
const DefaultRate = 512 * 1024

func init() {
	spec, _ := syscall.Getenv("PIG_PROFILE")
	// pig additive (D627): heap-profile sampling is off unless the process asks for a profile.
	runtime.MemProfileRate = rateFor(runtime.MemProfileRate, spec)
}

// rateFor is the rate the process runs with, given the rate the runtime started with and the value of PIG_PROFILE. A rate other than
// DefaultRate came from GODEBUG=memprofilerate=N, the user's own request, and stays.
func rateFor(current int, profile string) int {
	if current != DefaultRate || wantsHeapSamples(profile) {
		return current
	}
	return 0
}

// wantsHeapSamples reports whether the comma-separated PIG_PROFILE value names a profile that reads the sampled allocation records.
func wantsHeapSamples(profile string) bool {
	for kind := range strings.SplitSeq(profile, ",") {
		if kind = strings.TrimSpace(kind); kind == "heap" || kind == "allocs" {
			return true
		}
	}
	return false
}
