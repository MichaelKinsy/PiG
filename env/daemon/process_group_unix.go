//go:build !windows

package daemon

// Ports packages/env/daemon/src/sys/unix.rs (Group)

import "os/exec"

// newGroup is the group of a program just started; programAttributes already made it a process group of its own.
func newGroup(cmd *exec.Cmd) (*programGroup, error) {
	pid := cmd.Process.Pid
	return &programGroup{pid: pid, live: true, ending: func() { killTree(pid) }}, nil
}
