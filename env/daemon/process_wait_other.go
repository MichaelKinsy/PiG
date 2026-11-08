//go:build !linux && !darwin && !windows

package daemon

import "syscall"

// waitForExit fails: this system has no way to wait for a program without reaping it, so the program is reported as failing
// to start.
func (g *programGroup) waitForExit() error { return syscall.ENOSYS }
