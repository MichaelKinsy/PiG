//go:build unix

package extension

import (
	"os"
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
