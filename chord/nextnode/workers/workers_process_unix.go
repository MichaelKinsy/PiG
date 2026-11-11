//go:build unix

package workers

import (
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/unix"
)

// processTree is the process group a worker leads: killing it kills everything the worker spawned.
type processTree struct{ pid int }

// detach makes cmd lead a process group of its own (Node's `detached: true`).
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// treeOf is the tree of the started cmd.
func treeOf(cmd *exec.Cmd) (*processTree, error) { return &processTree{pid: cmd.Process.Pid}, nil }

// kill kills the process group; false when there is none.
func (tree *processTree) kill() bool {
	if tree == nil || tree.pid <= 0 {
		return false
	}
	err := syscall.Kill(-tree.pid, syscall.SIGKILL)
	return err == nil
}

func (tree *processTree) close() {}

// exitOf is how a process ended: its signal, or its exit code.
func exitOf(state *os.ProcessState) Exit {
	if state == nil {
		return Exit{}
	}
	if status, ok := state.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return Exit{Signal: new(unix.SignalName(status.Signal()))}
	}
	return Exit{Code: new(state.ExitCode())}
}

// killedExit is the exit of a process Kill ended: the kernel reported its signal already.
func killedExit(exit Exit) Exit { return exit }
