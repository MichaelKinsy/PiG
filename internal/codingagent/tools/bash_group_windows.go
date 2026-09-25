//go:build windows

package tools

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

// setProcessGroup is a no-op on Windows. Upstream spawns bash with
// detached:false on win32; the tree is reaped by killProcessGroup via taskkill.
func setProcessGroup(_ *exec.Cmd) {}

// killProcessGroup kills the process tree with `taskkill /F /T`, the Windows
// analog of unix's negative-pid SIGKILL. Mirrors upstream killProcessTree
// (utils/shell.ts). Falls back to a single-process terminate if taskkill fails.
func killProcessGroup(p *os.Process) error {
	kill := exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(p.Pid))
	kill.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := kill.Run(); err != nil {
		return p.Kill()
	}
	return nil
}

// shellExitCode returns the process exit code. Windows processes end with an
// exit code; upstream's fallback for a missing one is 1.
func shellExitCode(state *os.ProcessState) int {
	if code := state.ExitCode(); code >= 0 {
		return code
	}
	return 1
}
