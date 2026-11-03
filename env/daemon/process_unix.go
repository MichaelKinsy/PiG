//go:build !windows

package daemon

// Ports packages/env/daemon/src/sys/unix.rs

import (
	"os/exec"
	"syscall"

	"github.com/MichaelKinsy/PiG/internal/nodespawn"
)

// programAttributes starts the program as the leader of a new session, so a kill reaches every descendant that stays in
// its group (sys/unix.rs configure).
func programAttributes() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setsid: true} }

// startCommand starts cmd with an empty blocked-signal mask, as libuv starts programs.
func startCommand(cmd *exec.Cmd) error {
	return withEmptySignalMask(func() error { return nodespawn.Start(cmd) })
}

// killTree kills the program's process group, or the process if the group is gone.
func killTree(pid int) {
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}
