//go:build !windows

package packagemanager

import (
	"fmt"
	"os"
	"os/exec"
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

// A descendant can leave the child's process group (setsid) and still hold the output pipes, so no group tells Wait that none does; the pipes
// themselves do (internal/childwait).
func prepareCaptureGroup(*exec.Cmd)              {}
func attachCaptureGroup(*os.Process)             {}
func releaseCaptureGroup(*os.Process)            {}
func captureGroupMayHoldOutput(*os.Process) bool { return true }
