//go:build darwin

package main

import (
	"fmt"
	"os"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

// openModePTY allocates a Darwin pseudo-terminal pair (posix_openpt,
// grantpt, unlockpt). A cloned /dev/ptmx master's minor number is the index
// of its /dev/ttysNNN slave, which is what ptsname returns.
func openModePTY(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatalf("open /dev/ptmx: %v", err)
	}
	fd := int(master.Fd())
	for _, req := range []uint{unix.TIOCPTYGRANT, unix.TIOCPTYUNLK} {
		if err := unix.IoctlSetInt(fd, req, 0); err != nil {
			t.Fatalf("prepare pseudo-terminal: %v", err)
		}
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		t.Fatalf("stat pseudo-terminal master: %v", err)
	}
	path := fmt.Sprintf("/dev/ttys%03d", unix.Minor(uint64(stat.Rdev)))
	slave, err := os.OpenFile(path, os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatalf("open pseudo-terminal slave %s: %v", path, err)
	}
	return master, slave
}
