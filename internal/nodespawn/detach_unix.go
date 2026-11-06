//go:build unix

package nodespawn

import (
	"os/exec"
	"syscall"
)

// DetachFromTerminal starts cmd in a new session, which has no controlling
// terminal: a program that opens /dev/tty (FDclone does) fails instead of
// taking over PiG's screen. The session leads its own process group, so a
// cancelled cmd takes its descendants with it. cmd must come from
// exec.CommandContext (Start rejects a Cancel on any other), and its stdin,
// stdout and stderr must not be the terminal.
func DetachFromTerminal(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setsid = true
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
}
