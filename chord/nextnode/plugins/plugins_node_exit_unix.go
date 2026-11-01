//go:build !windows

package plugins

import (
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// processExit is Node's `exit` event for a process that ended: its exit code, or -1 and the name of the signal that ended it
// (`SIGKILL`).
func processExit(state *os.ProcessState) (code int, signal string) {
	if state == nil {
		return -1, ""
	}
	if status, ok := state.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return -1, unix.SignalName(status.Signal())
	}
	return state.ExitCode(), ""
}
