//go:build windows

package tools

import (
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"

	"github.com/MichaelKinsy/PiG/internal/nodespawn"
)

// setProcessGroup is a no-op on Windows. Upstream spawns bash with
// detached:false on win32; the tree is reaped by killProcessGroup via taskkill.
func setProcessGroup(_ *exec.Cmd) {}

// windowsTaskkillCommand detaches the hidden cleanup process from the caller's
// console, as upstream's spawn with stdio "ignore", detached: true, and
// windowsHide: true does.
func windowsTaskkillCommand(pid int) *exec.Cmd {
	command := newTaskkillCommand(pid)
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS}
	nodespawn.HideWindow(command, nodespawn.Ignore, nodespawn.Ignore, nodespawn.Ignore)
	return command
}

// killProcessGroup uses the trusted System32 taskkill and consumes failures without switching to a single-process kill.
// upstream: packages/coding-agent/src/utils/shell.ts:killProcessTree
func killProcessGroup(p *os.Process) error {
	runTaskkill(windowsTaskkillCommand(p.Pid), (*exec.Cmd).Run)
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
