//go:build unix

package aotruntime

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// fileLock is an advisory lock on a file, held until Unlock.
type fileLock struct{ file *os.File }

// lockFile takes the lock at path, waiting for it when wait is true. It returns false when another process holds it and wait is false.
func lockFile(path string, wait bool) (*fileLock, bool, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, false, err
	}
	how := syscall.LOCK_EX
	if !wait {
		how |= syscall.LOCK_NB
	}
	for {
		err = syscall.Flock(int(file.Fd()), how)
		if !errors.Is(err, syscall.EINTR) {
			break
		}
	}
	if err != nil {
		_ = file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return &fileLock{file: file}, true, nil
}

func (l *fileLock) Unlock() {
	_ = syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	_ = l.file.Close()
}

// ownProcessGroup starts cmd in a process group of its own and kills the whole group when its context ends, so a build leaves no compiler thread or
// host process behind. WaitDelay bounds the wait for output pipes that a process outside the group still holds.
func ownProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = processWaitDelay
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}

// endProcessGroup kills what is left of the process group of cmd once its leader has exited: processes the compiler or an extension factory started.
func endProcessGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
