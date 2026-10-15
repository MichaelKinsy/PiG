//go:build !windows

package testenv

import (
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"time"
)

type tree struct{ pgid int }

func startTree(cmd *exec.Cmd) (*tree, error) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	attr := cmd.SysProcAttr
	if attr.Pgid != 0 {
		return nil, fmt.Errorf("testenv: %v joins process group %d; a contained child leads its own group", cmd.Args, attr.Pgid)
	}
	// A session leader (Setsid, as a pseudo-terminal test starts its child) already leads a group whose ID is its PID,
	// and setpgid on a session leader fails with EPERM.
	if !attr.Setsid {
		attr.Setpgid = true
	}
	tr := &tree{}
	if cmd.Cancel != nil { // exec accepts a Cancel only on a command made by CommandContext.
		// exec calls Cancel from its context watcher, which can run before Start returns to this function, so the group
		// is read from cmd.Process (set before the watcher starts), never from tr.pgid: a zero pgid would make the kill
		// below signal the test's own process group.
		cmd.Cancel = func() error {
			pid := cmd.Process.Pid
			if pid <= 0 {
				return cmd.Process.Kill()
			}
			err := syscall.Kill(-pid, syscall.SIGKILL)
			if err != nil && !errors.Is(err, syscall.ESRCH) {
				return cmd.Process.Kill()
			}
			return nil
		}
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	tr.pgid = cmd.Process.Pid
	return tr, nil
}

func (tr *tree) alive() bool {
	err := syscall.Kill(-tr.pgid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// stop ends every process of the group: SIGTERM, then SIGKILL once grace has passed. It reports whether any member was
// running when it began.
func (tr *tree) stop(grace time.Duration) bool {
	if !tr.alive() {
		return false
	}
	_ = syscall.Kill(-tr.pgid, syscall.SIGTERM)
	if !tr.waitGone(grace) {
		_ = syscall.Kill(-tr.pgid, syscall.SIGKILL)
		tr.waitGone(spawnGrace)
	}
	return true
}

func (tr *tree) waitGone(limit time.Duration) bool {
	deadline := time.Now().Add(limit)
	for tr.alive() {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(5 * time.Millisecond)
	}
	return true
}
