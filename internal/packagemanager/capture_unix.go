//go:build !windows

package packagemanager

import (
	"fmt"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// Ports packages/coding-agent/src/core/package-manager.ts (spawnCaptureCommand, runCommandCapture child termination and exit status).
// Node child.kill() defaults to SIGTERM and leaves settlement to the close event.
func TerminatePackageCapture(process *os.Process) {
	_ = process.Signal(syscall.SIGTERM)
}

func PackageCaptureExitStatus(state *os.ProcessState) string {
	if status, ok := state.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return "signal " + unix.SignalName(status.Signal())
	}
	return fmt.Sprintf("code %d", state.ExitCode())
}
