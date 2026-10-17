// SPDX-License-Identifier: MIT

// Package procpolicy is PiG's GOMAXPROCS policy. pig additive (D174): a process whose environment does not set GOMAXPROCS runs with at
// most Cap Ps, so the streaming hand-offs of a turn do not wake and park idle Ps, and each parallel section of a session load runs at the
// runtime default width while it lasts.
package procpolicy

import (
	"os"
	"runtime"
	"sync"
	"sync/atomic"
)

// Cap is the most Ps the process runs on outside a parallel section.
const Cap = 2

var (
	// capped is set once Apply has lowered GOMAXPROCS; Widen does nothing until then.
	capped atomic.Bool
	// mu orders the GOMAXPROCS transitions; sections counts the open parallel sections.
	mu       sync.Mutex
	sections int
)

// Apply caps GOMAXPROCS at min(Cap, the runtime default) when the environment does not set GOMAXPROCS. A user's GOMAXPROCS (and GOGC,
// which the policy never reads) is left alone, and so is a runtime default of Cap or less, which keeps the runtime's automatic updates.
// It runs before anything else reads or sets GOMAXPROCS, so the width it reads is the runtime default (container-aware since Go 1.25).
func Apply() {
	if _, set := os.LookupEnv("GOMAXPROCS"); set {
		return
	}
	mu.Lock()
	defer mu.Unlock()
	if capped.Load() || runtime.GOMAXPROCS(0) <= Cap {
		return
	}
	runtime.GOMAXPROCS(Cap)
	capped.Store(true)
}

// Widen opens a parallel section: when Apply capped GOMAXPROCS, the first open section restores the runtime default
// (runtime.SetDefaultGOMAXPROCS, which recomputes it from the CPU count, affinity and cgroup limit and resumes the runtime's automatic
// updates), so a worker count read from runtime.GOMAXPROCS(0) inside the section is the default width. It returns the function that closes
// the section; the caller defers it, so a panicking section closes too. When the last open section closes, GOMAXPROCS goes back to
// min(Cap, the default at that moment). Sections may nest and run concurrently. Without the cap both are no-ops.
func Widen() (narrow func()) {
	if !capped.Load() {
		return inert
	}
	mu.Lock()
	if sections++; sections == 1 {
		runtime.SetDefaultGOMAXPROCS()
	}
	mu.Unlock()
	return closeSection
}

func closeSection() {
	mu.Lock()
	if sections--; sections == 0 {
		runtime.GOMAXPROCS(min(Cap, runtime.GOMAXPROCS(0)))
	}
	mu.Unlock()
}

func inert() {}
