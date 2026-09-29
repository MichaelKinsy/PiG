//go:build !windows

package experimental

import (
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func workerSignalName(state *os.ProcessState) string {
	if status, ok := state.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return unix.SignalName(status.Signal())
	}
	return ""
}
