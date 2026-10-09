//go:build darwin

package tui

import (
	"os"
	"syscall"
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"
)

// openTestPTY returns the master and slave ends of a new pseudo-terminal.
func openTestPTY(t *testing.T) (master, slave *os.File) {
	t.Helper()
	fd, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Skipf("no pseudo-terminal: %v", err)
	}
	master = os.NewFile(uintptr(fd), "/dev/ptmx")
	if err := unix.IoctlSetInt(fd, unix.TIOCPTYGRANT, 0); err != nil {
		t.Fatal(err)
	}
	if err := unix.IoctlSetInt(fd, unix.TIOCPTYUNLK, 0); err != nil {
		t.Fatal(err)
	}
	var name [128]byte
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), uintptr(unix.TIOCPTYGNAME), uintptr(unsafe.Pointer(&name[0]))); errno != 0 { //nolint:gosec // G103: TIOCPTYGNAME writes a NUL-terminated path into the 128-byte buffer.
		t.Fatal(errno)
	}
	end := 0
	for end < len(name) && name[end] != 0 {
		end++
	}
	slave, err = os.OpenFile(string(name[:end]), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = slave.Close(); _ = master.Close() })
	return master, slave
}

const ttyGetAttributes = unix.TIOCGETA
