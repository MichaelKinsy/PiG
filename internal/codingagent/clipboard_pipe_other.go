//go:build !windows

package codingagent

import (
	"os"
	"os/exec"
)

func hideClipboardWindow(*exec.Cmd) {}

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
