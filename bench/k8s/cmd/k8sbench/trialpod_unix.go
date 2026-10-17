//go:build unix

package main

import (
	"os"
	"syscall"
)

// groupAttr puts a trial's child in its own process group, so killGroup also ends what it started.
func groupAttr() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setpgid: true} }

func killGroup(p *os.Process) { _ = syscall.Kill(-p.Pid, syscall.SIGKILL) }
