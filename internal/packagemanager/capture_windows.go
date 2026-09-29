//go:build windows

package packagemanager

import (
	"fmt"
	"os"
)

// Windows implements Node child.kill() as process termination.
func TerminatePackageCapture(process *os.Process) {
	_ = process.Kill()
}

func PackageCaptureExitStatus(state *os.ProcessState) string {
	return fmt.Sprintf("code %d", state.ExitCode())
}
