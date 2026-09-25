//go:build !windows

package experimental

import "syscall"

func internalProcessAttributes() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
