//go:build unix

package extension

import "syscall"

// newProcAttr detaches the child into a new process group (Setpgid) so a
// timeout/cancel can target the tree without signalling pig itself.
func newProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}
