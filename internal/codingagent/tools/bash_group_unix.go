//go:build unix

package tools

import (
	"os"
	"os/exec"
	"runtime"
	"syscall"
)

// setProcessGroup puts the command in its own process group so a group-kill
// reaps grandchildren.
// Ports packages/coding-agent/src/utils/shell.ts
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// attachProcessGroup and releaseProcessGroup are no-ops: the process group is
// set before the start and needs no handle.
func attachProcessGroup(*os.Process)  {}
func releaseProcessGroup(*os.Process) {}

// killProcessGroup ends the whole process group (negative pid).
// Ports packages/coding-agent/src/utils/shell.ts:killProcessTree (Unix branch).
func killProcessGroup(p *os.Process) error {
	return killProcessTree(syscall.Kill, runtime.GOOS == "darwin", p.Pid)
}

// killProcessTree stops the group first on macOS, then SIGKILLs it and falls back to the shell alone when the group
// kill fails. macOS signals group members one at a time after releasing the group lock, so a killed child can wake its
// waiting shell before the shell is signalled and let `a; b` start b during cancellation (Pi #11252). Stopping first
// narrows that race; it is best-effort, not an atomic freeze, and a failed stop never prevents the kill. SIGKILL also
// ends stopped processes. Linux keeps the single group SIGKILL because its group signalling holds the lock that child
// exit notification needs.
func killProcessTree(kill func(pid int, sig syscall.Signal) error, darwin bool, pid int) error {
	if darwin {
		_ = kill(-pid, syscall.SIGSTOP)
	}
	err := kill(-pid, syscall.SIGKILL)
	if err == nil {
		return nil
	}
	return kill(pid, syscall.SIGKILL)
}

// processGroupMayHoldOutput reports true: a detached descendant can leave the
// process group (setsid) and still hold the pipe, so upstream's grace applies.
// Whether a descendant does is what the pipe itself tells childwait.Wait
// (poll(2): no write end left, or bytes still unread); the process group does not.
func processGroupMayHoldOutput(*os.Process) bool { return true }

// shellExitCode mirrors upstream createLocalShellOperations: a shell killed by
// a signal has no exit code, so it reports 128 + the signal number rather
// than a value callers could mistake for success.
func shellExitCode(state *os.ProcessState) int {
	if status, ok := state.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return 128 + int(status.Signal())
	}
	return state.ExitCode()
}
