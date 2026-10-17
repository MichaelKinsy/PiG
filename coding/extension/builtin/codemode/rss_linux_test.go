package codemode

import "syscall"

// ru_maxrss is in KiB on linux.
func peakRSSOf(r *syscall.Rusage) uint64 { return uint64(r.Maxrss) << 10 }
