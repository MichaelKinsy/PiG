//go:build !windows

package packagemanager

import (
	"os/exec"
	"syscall"
)

// detachForTest starts cmd in its own session, as spawn(..., { detached: true }) does.
func detachForTest(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
