//go:build integration && linux

package integration

import (
	"fmt"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// openHangupPTY opens a pseudo-terminal pair sized 120x34. The master is a pollable file, so closing it while a reader waits hangs the terminal up.
func openHangupPTY() (master, slave *os.File, err error) {
	master, err = os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, err
	}
	fail := func(err error) (*os.File, *os.File, error) {
		_ = master.Close()
		return nil, nil, err
	}
	if err := unix.IoctlSetPointerInt(int(master.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		return fail(err)
	}
	number, err := unix.IoctlGetInt(int(master.Fd()), unix.TIOCGPTN)
	if err != nil {
		return fail(err)
	}
	slave, err = os.OpenFile(fmt.Sprintf("/dev/pts/%d", number), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return fail(err)
	}
	if err := unix.IoctlSetWinsize(int(master.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: 34, Col: 120}); err != nil {
		_ = slave.Close()
		return fail(err)
	}
	return master, slave, nil
}
