//go:build !unix

package models

import "os/exec"

func inGroup(*exec.Cmd) {}

// killGroup ends the shell only (there are no process groups to signal).
func killGroup(cmd *exec.Cmd) { _ = cmd.Process.Kill() }
