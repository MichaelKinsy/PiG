//go:build !windows

package codingagent

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/unix"
)

// exitSignalName is Node's name for the signal that ended the command, such as SIGTERM, or "" when it exited.
func exitSignalName(err *exec.ExitError) string {
	if status, ok := err.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return unix.SignalName(status.Signal())
	}
	return ""
}
