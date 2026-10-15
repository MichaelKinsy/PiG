//go:build darwin

package daemon

// Ports packages/env/daemon/src/pty.rs

import (
	"bytes"
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// openPty is openpty(3): the master of a new terminal, whose reads and writes wait through the runtime's poller, and
// its slave. The daemon is built without cgo, so it does what libc's openpty does: grantpt and unlockpt on the master
// and the slave's name from it.
func openPty() (master, slave *os.File, err error) {
	fd, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, &os.PathError{Op: "open", Path: "/dev/ptmx", Err: err}
	}
	master = os.NewFile(uintptr(fd), "/dev/ptmx")
	var name [128]byte
	for _, step := range []struct {
		request  uintptr
		argument unsafe.Pointer
	}{
		{syscall.TIOCPTYGRANT, nil},
		{syscall.TIOCPTYUNLK, nil},
		{syscall.TIOCPTYGNAME, unsafe.Pointer(&name[0])}, //nolint:gosec // G103: TIOCPTYGNAME writes the pty name into the caller's buffer through the ioctl argument; the daemon is built without cgo
	} {
		if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), step.request, uintptr(step.argument)); errno != 0 {
			_ = master.Close()
			return nil, nil, &os.PathError{Op: "ioctl", Path: "/dev/ptmx", Err: errno}
		}
	}
	path := ""
	for index, character := range name {
		if character == 0 {
			path = string(name[:index])
			break
		}
	}
	slaveFD, err := unix.Open(path, unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		_ = master.Close()
		return nil, nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return master, os.NewFile(uintptr(slaveFD), path), nil
}

// execPathDefault is where execvp looks for a program whose environment has no PATH: _PATH_DEFPATH of macOS's libc.
const execPathDefault = "/usr/bin:/bin"

const (
	// procInfoCallPidInfo and procPidTBSDInfo select proc_info's PIDINFO call with the BSD info flavor, which libproc's
	// proc_pidinfo(pid, PROC_PIDTBSDINFO, ...) issues.
	procInfoCallPidInfo = 2
	procPidTBSDInfo     = 3
	// bsdInfoSize is sizeof(struct proc_bsdinfo); pbi_comm (16 bytes) starts at 48 and pbi_name (32 bytes) at 64.
	bsdInfoSize = 136
	bsdInfoComm = 48
	bsdInfoName = 64
	maxComLen   = 16
)

// cString is the text of buffer up to its first NUL.
func cString(buffer []byte) string {
	if end := bytes.IndexByte(buffer, 0); end >= 0 {
		buffer = buffer[:end]
	}
	return string(buffer)
}

// processName is libproc's proc_name: the process's long name (up to 32 bytes), else its short command name, if it
// still runs and the system tells. The daemon is built without cgo, so it makes the system call proc_name makes.
func processName(pid int) (string, bool) {
	var info [bsdInfoSize]byte
	read, _, errno := unix.Syscall6(unix.SYS_PROC_INFO, procInfoCallPidInfo, uintptr(pid), procPidTBSDInfo, 0, //nolint:staticcheck // SYS_PROC_INFO has no libSystem wrapper reachable without cgo, and the daemon is built without cgo
		uintptr(unsafe.Pointer(&info[0])), bsdInfoSize) //nolint:gosec // G103: proc_info fills the caller's buffer through the syscall argument; the daemon is built without cgo
	if errno != 0 || read != bsdInfoSize {
		return "", false
	}
	name := cString(info[bsdInfoName : bsdInfoName+2*maxComLen])
	if name == "" {
		name = cString(info[bsdInfoComm : bsdInfoComm+maxComLen])
	}
	return name, name != ""
}
