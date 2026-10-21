//go:build !windows

package codingagent

import (
	"os"
	"os/exec"
	"syscall"
)

func hideClipboardWindow(*exec.Cmd) {}

// detachClipboardChild is Node's spawn option `detached: process.platform !== "win32"` (libuv setsid). A clipboard daemon that keeps serving
// the selection then leaves pi's process group, so Ctrl+Z (SIGTSTP to the foreground group) does not stop it (#11179).
// Ports packages/coding-agent/src/utils/clipboard-command.ts:runClipboardCommand.
func detachClipboardChild(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// clipboardPipe returns a connected byte pipe for a clipboard command's stdin or stdout. The parent end uses the runtime poller, so closing it cancels a pending read or write.
func clipboardPipe(parentReads bool) (parentEnd, childEnd *os.File, err error) {
	read, write, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}
	if parentReads {
		return read, write, nil
	}
	return write, read, nil
}
