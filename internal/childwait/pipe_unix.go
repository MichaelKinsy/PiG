//go:build unix

package childwait

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// pollPipe asks the kernel about the read end of a pipe without taking a byte from it or changing its mode (SyscallConn, not Fd, which would make the
// descriptor blocking under the reader). hungUp reports that every write end is closed, which poll(2) reports as POLLHUP on a pipe whether or not
// bytes remain in it; unread reports that bytes wait in the pipe while a write end is still open (an ended pipe reports POLLIN on macOS, where
// the end of the stream is readable, so unread excludes a hung-up pipe). known is false when the kernel did not answer.
func pollPipe(file *os.File) (hungUp, unread, known bool) {
	conn, err := file.SyscallConn()
	if err != nil {
		return false, false, false
	}
	fds := []unix.PollFd{{Events: unix.POLLIN}}
	var pollErr error
	if err := conn.Control(func(fd uintptr) {
		fds[0].Fd = int32(fd)
		for {
			if _, pollErr = unix.Poll(fds, 0); !errors.Is(pollErr, unix.EINTR) {
				return
			}
		}
	}); err != nil || pollErr != nil {
		return false, false, false
	}
	revents := fds[0].Revents
	if revents&(unix.POLLERR|unix.POLLNVAL) != 0 {
		return false, false, false
	}
	hungUp = revents&unix.POLLHUP != 0
	return hungUp, !hungUp && revents&unix.POLLIN != 0, true
}
