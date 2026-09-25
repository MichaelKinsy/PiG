//go:build windows

package rpcclient

import (
	"os"
	"strconv"
)

// terminateProcess mirrors Node's kill("SIGTERM"), which terminates the
// process outright on Windows.
func terminateProcess(process *os.Process) {
	_ = process.Kill()
}

// exitCodeAndSignal renders Node's exit (code, signal) pair; Windows exits
// always carry a code.
func exitCodeAndSignal(state *os.ProcessState) (string, string) {
	if state == nil {
		return "null", "null"
	}
	return strconv.Itoa(state.ExitCode()), "null"
}
