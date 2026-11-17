//go:build windows

package packagemanager

import (
	"os/exec"
	"syscall"
)

// detachForTest starts cmd detached from its parent's console, as spawn(..., { detached: true }) does.
func detachForTest(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | 0x00000008}
}
