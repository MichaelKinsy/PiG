//go:build windows

package plugins

import "os"

// processExit is Node's `exit` event for a process that ended: its exit code, or -1 and `SIGKILL` for one the Go side killed.
// Windows has no signals: a killed process has the exit code the kill gave it (1), indistinguishable from an exit(1), so killed
// tells what the status cannot, and the exit is the one a unix gives the same kill.
func processExit(state *os.ProcessState, killed bool) (code int, signal string) {
	if state == nil {
		return -1, ""
	}
	if killed {
		return -1, "SIGKILL"
	}
	return state.ExitCode(), ""
}
