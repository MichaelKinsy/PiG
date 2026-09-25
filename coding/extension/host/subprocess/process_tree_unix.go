//go:build !windows

package subprocess

import (
	"errors"
	"os"
	"os/exec"
	"sync"
	"syscall"
)

type processTree struct {
	process *os.Process
	once    sync.Once
	err     error
}

func startProcessTree(cmd *exec.Cmd) (*processTree, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return killProcessGroup(cmd.Process)
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &processTree{process: cmd.Process}, nil
}

func (tree *processTree) Kill() error {
	if tree == nil || tree.process == nil {
		return nil
	}
	tree.once.Do(func() { tree.err = killProcessGroup(tree.process) })
	return tree.err
}

func (tree *processTree) Close() error { return tree.Kill() }

func killProcessGroup(process *os.Process) error {
	err := syscall.Kill(-process.Pid, syscall.SIGKILL)
	if err == nil || errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return process.Kill()
}
