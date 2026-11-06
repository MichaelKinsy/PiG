//go:build unix

package nodespawn

import (
	"os/exec"
	"syscall"
)

// DetachFromTerminal starts cmd in a new session, which has no controlling
// terminal: a program that opens /dev/tty (FDclone does) fails instead of
// taking over PiG's screen.
func DetachFromTerminal(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setsid = true
}
