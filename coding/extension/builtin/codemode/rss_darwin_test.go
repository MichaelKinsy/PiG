package codemode

import "syscall"

// ru_maxrss is in bytes on darwin.
func peakRSSOf(r *syscall.Rusage) uint64 { return uint64(r.Maxrss) }
