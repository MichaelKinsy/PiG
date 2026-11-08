//go:build !windows

package terminals

import (
	"os/exec"
	"syscall"
)

// detach makes the command a process of its own session, as Node's `detached: true`.
func detach(command *exec.Cmd) { command.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
