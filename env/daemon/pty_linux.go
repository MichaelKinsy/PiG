//go:build linux

package daemon

// Ports packages/env/daemon/src/pty.rs

import (
	"os"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

// openPty is openpty(3): the master of a new terminal, whose reads and writes wait through the runtime's poller, and
// its slave.
func openPty() (master, slave *os.File, err error) {
	fd, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, &os.PathError{Op: "open", Path: "/dev/ptmx", Err: err}
	}
	master = os.NewFile(uintptr(fd), "/dev/ptmx")
	number, err := unix.IoctlGetInt(fd, unix.TIOCGPTN)
	if err == nil {
		err = unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0)
	}
	if err != nil {
		_ = master.Close()
		return nil, nil, &os.PathError{Op: "ioctl", Path: "/dev/ptmx", Err: err}
	}
	name := "/dev/pts/" + strconv.Itoa(number)
	slaveFD, err := unix.Open(name, unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		_ = master.Close()
		return nil, nil, &os.PathError{Op: "open", Path: name, Err: err}
	}
	return master, os.NewFile(uintptr(slaveFD), name), nil
}

// execPathDefault is where execvp looks for a program whose environment has no PATH: musl's default, the C library of
// Pi's Linux daemons (x86_64/aarch64-unknown-linux-musl in .github/workflows/env-daemons.yml).
const execPathDefault = "/usr/local/bin:/bin:/usr/bin"

// processName is a process's name, if it still runs and the system tells. Like pty.rs's read_to_string, a name that is
// not UTF-8 (the kernel cuts it at 15 bytes, which can split a character) is no name.
func processName(pid int) (string, bool) {
	name, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/comm")
	if err != nil || !utf8.Valid(name) {
		return "", false
	}
	text := strings.TrimRightFunc(string(name), unicode.IsSpace)
	return text, text != ""
}
