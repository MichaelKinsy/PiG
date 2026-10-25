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

// A descendant can leave the child's process group (setsid) and still hold the output pipes, so no group tells Wait that none does; the pipes
// themselves do (internal/childwait).
func prepareChildGroup(*exec.Cmd)              {}
func attachChildGroup(*os.Process)             {}
func releaseChildGroup(*os.Process)            {}
func childGroupMayHoldOutput(*os.Process) bool { return true }
