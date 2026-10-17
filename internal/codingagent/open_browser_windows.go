//go:build windows

package codingagent

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// detachLauncher is Node's spawn option `detached: true`: libuv adds DETACHED_PROCESS and CREATE_NEW_PROCESS_GROUP.
func detachLauncher(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS}
}
