//go:build unix

package cli

import (
	"errors"
	"syscall"
)

// extensionProcessAlive reports whether pid still names a process, reaped or not: the host reaps a process it waited for.
func extensionProcessAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
