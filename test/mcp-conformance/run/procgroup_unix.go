//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// ownProcessGroup starts the command in its own process group, so the runner can end the client and the scenario
// servers it spawns together.
func ownProcessGroup(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

// killProcessGroup ends the whole group of a started command.
func killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}

const supported = true
