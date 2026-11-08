//go:build !windows

package daemon

import (
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// pollable returns f on a duplicate descriptor that the runtime's poller waits on, and the function that closes the
// duplicate. A pipe or socket the process inherited (stdin and stdout of the daemon) is in blocking mode, so a Go
// goroutine reading or writing it holds a scheduler slot until sysmon takes the slot back, up to 10 ms later; with every
// slot held by a blocked read or write, a request that has just arrived waits for that long before a worker runs it.
// Pi's daemon blocks an operating system thread there, which costs the scheduler nothing, so only the poller wait keeps the
// reply latency the same. Any other file, or a descriptor already in the poller, is returned unchanged.
func pollable(f *os.File) (*os.File, func()) {
	var duplicate int
	duplicated, wasBlocking := false, false
	conn, err := f.SyscallConn()
	if err != nil {
		return f, func() {}
	}
	_ = conn.Control(func(fd uintptr) {
		var stat unix.Stat_t
		if unix.Fstat(int(fd), &stat) != nil {
			return
		}
		if kind := stat.Mode & unix.S_IFMT; kind != unix.S_IFIFO && kind != unix.S_IFSOCK {
			return
		}
		wasBlocking, err = isBlocking(int(fd))
		if err != nil {
			return
		}
		next, err := unix.FcntlInt(fd, unix.F_DUPFD_CLOEXEC, 0)
		if err != nil {
			return
		}
		if unix.SetNonblock(next, true) != nil {
			_ = unix.Close(next)
			return
		}
		duplicate, duplicated = next, true
	})
	if !duplicated {
		return f, func() {}
	}
	wrapped := os.NewFile(uintptr(duplicate), f.Name())
	return wrapped, func() {
		// The mode belongs to the open file description, which the daemon's parent and other descriptors share: leave it as found.
		if wasBlocking {
			_ = unix.SetNonblock(duplicate, false)
		}
		_ = wrapped.Close()
	}
}

// pollableReader and pollableWriter apply pollable to the standard streams the daemon is given.
func pollableReader(reader io.Reader) (io.Reader, func()) {
	if f, ok := reader.(*os.File); ok {
		return pollable(f)
	}
	return reader, func() {}
}

func pollableWriter(writer io.Writer) (io.Writer, func()) {
	if f, ok := writer.(*os.File); ok {
		return pollable(f)
	}
	return writer, func() {}
}

func isBlocking(fd int) (bool, error) {
	flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
	return flags&unix.O_NONBLOCK == 0, err
}
