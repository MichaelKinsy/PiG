//go:build !windows

package codingagent

import (
	"os/exec"
	"syscall"
)

// detachLauncher is Node's spawn option `detached: true`: libuv starts the child with setsid.
func detachLauncher(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
