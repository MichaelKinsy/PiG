//go:build unix

package extensionconformance

import (
	"errors"
	"syscall"
)

// processGone reports whether no process has the pid. The host has waited for its child, so a command that the abort ended is gone.
func processGone(pid int) bool { return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) }

func killProcess(pid int) { _ = syscall.Kill(pid, syscall.SIGKILL) }
