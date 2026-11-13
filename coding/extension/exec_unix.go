//go:build unix

package extension

import (
	"os"
	"os/exec"
	"syscall"
)

// terminate mirrors upstream's proc.kill("SIGTERM"): it signals the child and
// nothing else. It reports false: whether the child then has an exit code
// depends on how it ends, which its ProcessState reports.
func terminate(process *os.Process) (noExitCode bool) {
	// The child may already have exited; there is nothing to signal.
	_ = process.Signal(syscall.SIGTERM)
	return false
}

// forceTerminate is proc.kill("SIGKILL"), which exec.ts sends execForceKillDelay after SIGTERM to a child that has not exited.
func forceTerminate(process *os.Process) { _ = process.Signal(syscall.SIGKILL) }

// exitCode is exec.ts's `code ?? (proc.signalCode ? 128 + osConstants.signals[proc.signalCode] : 1)`: the child's exit code, 128 + the number of the
// signal that ended it when it has none, and 1 when it has neither. The signal numbers are the platform's, which Node's os.constants.signals reports.
func exitCode(state *os.ProcessState, _ bool) int {
	if code := state.ExitCode(); code >= 0 {
		return code
	}
	if status, ok := state.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return 128 + int(status.Signal())
	}
	return 1
}

// A descendant can leave the child's process group (setsid) and still hold the output pipes, so no group tells Wait that none does; the pipes
// themselves do (internal/childwait).
func prepareChildGroup(*exec.Cmd)              {}
func attachChildGroup(*os.Process)             {}
func releaseChildGroup(*os.Process)            {}
func childGroupMayHoldOutput(*os.Process) bool { return true }
