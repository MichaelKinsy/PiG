//go:build windows

package nodespawn

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// DetachFromTerminal starts cmd without a console, the Windows counterpart of
// Setsid: the child cannot attach to PiG's console.
func DetachFromTerminal(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.DETACHED_PROCESS
}
