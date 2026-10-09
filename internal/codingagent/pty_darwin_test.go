//go:build darwin

package codingagent

import (
	"fmt"
	"os"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

// termiosRead is the ioctl that reads a terminal's attributes.
const termiosRead = unix.TIOCGETA

// openTestPTY opens a pseudo-terminal pair sized 100x30.
func openTestPTY(t *testing.T) (master, slave *os.File) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY|syscall.O_CLOEXEC, 0)
	if err != nil {
		t.Fatalf("open /dev/ptmx: %v", err)
	}
	t.Cleanup(func() { _ = master.Close() })
	fd := int(master.Fd())
	if err := unix.IoctlSetInt(fd, unix.TIOCPTYGRANT, 0); err != nil {
		t.Fatalf("grant pseudo-terminal: %v", err)
	}
	if err := unix.IoctlSetInt(fd, unix.TIOCPTYUNLK, 0); err != nil {
		t.Fatalf("unlock pseudo-terminal: %v", err)
	}
	// The slave of the master with minor number n is /dev/ttys<n>, as
	// ptsname reports it on macOS.
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		t.Fatalf("stat pseudo-terminal: %v", err)
	}
	path := fmt.Sprintf("/dev/ttys%03d", unix.Minor(uint64(stat.Rdev)))
	slave, err = os.OpenFile(path, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatalf("open pseudo-terminal slave %s: %v", path, err)
	}
	t.Cleanup(func() { _ = slave.Close() })
	if err := unix.IoctlSetWinsize(fd, unix.TIOCSWINSZ, &unix.Winsize{Row: 30, Col: 100}); err != nil {
		t.Fatalf("set pseudo-terminal size: %v", err)
	}
	return master, slave
}
