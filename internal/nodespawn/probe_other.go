//go:build !linux && !windows

package nodespawn

import "syscall"

// probeExecve is not needed where execveErrno starts every executable regular
// file: it cannot ask the kernel.
func probeExecve(string, string) (syscall.Errno, bool) { return 0, false }
