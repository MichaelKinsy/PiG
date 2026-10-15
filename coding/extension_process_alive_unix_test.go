//go:build !windows

package coding

import (
	"errors"
	"syscall"
)

func extensionProcessAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
