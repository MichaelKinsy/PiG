//go:build windows

package main

import "os/exec"

func ownProcessGroup(*exec.Cmd) {}

func killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}

// supported is false: the runner needs a POSIX shell.
const supported = false
