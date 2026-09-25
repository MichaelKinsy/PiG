//go:build unix

package tools

import (
	"os"
	"os/exec"
	"syscall"
)

// setProcessGroup puts the command in its own process group so a group-kill
// reaps grandchildren.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessGroup SIGKILLs the whole process group (negative pid).
func killProcessGroup(p *os.Process) error {
	return syscall.Kill(-p.Pid, syscall.SIGKILL)
}

// shellExitCode mirrors upstream createLocalShellOperations: a shell killed by
// a signal has no exit code, so it reports 128 + the signal number rather
// than a value callers could mistake for success.
func shellExitCode(state *os.ProcessState) int {
	if status, ok := state.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return 128 + int(status.Signal())
	}
	return state.ExitCode()
}
