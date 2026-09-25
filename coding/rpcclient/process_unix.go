//go:build !windows

package rpcclient

import (
	"os"
	"strconv"
	"syscall"

	"golang.org/x/sys/unix"
)

// terminateProcess mirrors Node's kill("SIGTERM").
func terminateProcess(process *os.Process) {
	_ = process.Signal(syscall.SIGTERM)
}

// exitCodeAndSignal renders Node's exit (code, signal) pair: a normal exit has
// a code and a null signal; a signalled one has a null code and the signal's
// name, such as SIGTERM.
func exitCodeAndSignal(state *os.ProcessState) (string, string) {
	if state == nil {
		return "null", "null"
	}
	if status, ok := state.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return "null", unix.SignalName(status.Signal())
	}
	return strconv.Itoa(state.ExitCode()), "null"
}
