//go:build windows

package codingagent

import "syscall"

// terminateSelf ends the process abruptly, the Windows form of dying by a signal: there are no signals, so the parent, Node's close
// event included, sees exit code 1 as it does for any process another one terminates.
func terminateSelf() {
	if handle, err := syscall.GetCurrentProcess(); err == nil {
		_ = syscall.TerminateProcess(handle, 1)
	}
}
