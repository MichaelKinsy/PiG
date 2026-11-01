//go:build windows

package plugins

import "os"

// processExit is Node's `exit` event for a process that ended: its exit code. Windows has no signals: a killed process has
// the exit code the kill gave it.
func processExit(state *os.ProcessState) (code int, signal string) {
	if state == nil {
		return -1, ""
	}
	return state.ExitCode(), ""
}
