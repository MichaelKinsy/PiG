//go:build windows

package extension

import "syscall"

// newProcAttr puts the child in a new process group (CREATE_NEW_PROCESS_GROUP),
// the Windows analog of the unix Setpgid detach.
func newProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}
