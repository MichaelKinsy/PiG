//go:build darwin

package cli

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
	// The Linux helper opens its pseudo-terminal 24 rows by 80 columns; a zero-width terminal makes the interactive renderer emit one character per row.
	if err := unix.IoctlSetWinsize(fd, unix.TIOCSWINSZ, &unix.Winsize{Row: 24, Col: 80}); err != nil {
		t.Fatalf("size pseudo-terminal: %v", err)
	}
	return master, slave
}
