// SPDX-License-Identifier: MIT

// Package perfgate holds the whole-process count gates: a built pig runs the print, RPC and startup paths with PIG_PROFILE=metrics under GOMAXPROCS=1, and its exit record (allocations, bytes, GC cycles, live heap, peak RSS, goroutines, descriptors, child processes), plus the entries it left in its private TMPDIR, is checked against automation/perf/ratchet.toml. The stripped binary size of every release platform is gated the same way.
package perfgate
