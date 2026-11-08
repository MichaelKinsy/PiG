//go:build unix

package models

import (
	"os/exec"
	"syscall"
)

// inGroup starts cmd in a process group of its own.
func inGroup(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

// killGroup ends cmd's whole process group.
func killGroup(cmd *exec.Cmd) { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
