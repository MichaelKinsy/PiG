//go:build unix

package tui

import (
	"os"
	"os/exec"
	"syscall"
)

// openNoFollow opens file without blocking (a FIFO waits for no writer) and without following a link at the last step.
func openNoFollow(file string) (*os.File, error) {
	fd, err := syscall.Open(file, syscall.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), file), nil
}

// detach puts cmd in a session of its own: it outlives the launcher's terminal.
func detach(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
