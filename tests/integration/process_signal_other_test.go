//go:build integration && !windows

package integration

import (
	"os"
	"os/exec"
	"syscall"
)

// processRunning reports whether p has not exited.
func processRunning(p *os.Process) bool {
	return p.Signal(syscall.Signal(0)) == nil
}

// prepareTermination readies cmd, before it starts, for requestTermination.
func prepareTermination(*exec.Cmd) {}

// requestTermination asks p to stop the way a supervisor does, with SIGTERM,
// and returns the exit status pig reports for it: 128 plus the signal number.
func requestTermination(p *os.Process) (int, error) {
	return 128 + int(syscall.SIGTERM), p.Signal(syscall.SIGTERM)
}
