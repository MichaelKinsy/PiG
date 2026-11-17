//go:build windows

package packagemanager

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/MichaelKinsy/PiG/internal/jobgroup"
)

// Ports packages/coding-agent/src/core/package-manager.ts (spawnCaptureCommand, runCommandCapture child termination and exit status).
// Windows implements Node child.kill() as process termination.
func TerminatePackageCapture(process *os.Process) {
	_ = process.Kill()
}

func PackageCaptureExitStatus(state *os.ProcessState) string {
	return fmt.Sprintf("code %d", state.ExitCode())
}

// The child starts suspended and joins a job object before it runs, as the bash tool's shell does (internal/jobgroup), so that an empty job tells Wait
// that no process holds the output pipes. The job never kills anything.
func prepareCaptureGroup(cmd *exec.Cmd)                  { jobgroup.Suspend(cmd) }
func attachCaptureGroup(process *os.Process)             { jobgroup.Attach(process) }
func releaseCaptureGroup(process *os.Process)            { jobgroup.Release(process) }
func captureGroupMayHoldOutput(process *os.Process) bool { return jobgroup.MayHoldOutput(process) }
