//go:build unix

package childwait

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// pollPending reports whether the read end of a pipe is readable: it holds bytes, or has ended (POLLHUP with bytes also reports POLLIN). It is false when
// the kernel did not answer.
func pollPending(file *os.File) bool {
	conn, err := file.SyscallConn()
	if err != nil {
		return false
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
		return false
	}
	return fds[0].Revents&unix.POLLIN != 0
}

// pollSupported reports that the kernel answers poll(2) for a pipe.
const pollSupported = true

// pollReadable waits for the read end of a pipe to become readable (bytes, or the end of the stream). closed is true when the kernel says the
// descriptor is gone, as it does once Wait closed it. A pipe the runtime's poller watches is waited for without a clock: the wait ends when the pipe has
// something to read or ends, or when it is closed, and timeoutMS does not apply. Any other descriptor is polled for up to timeoutMS.
func pollReadable(file *os.File, timeoutMS int) (readable, closed bool) {
	if readable, closed, ok := awaitReadable(file); ok {
		return readable, closed
	}
	return pollReadableTimed(file, timeoutMS)
}

// awaitReadable waits for the runtime's poller to report the pipe readable. The callback asks the kernel first, so bytes that arrived before the wait
// began are seen, and again at each wakeup, because the poller reports edges. ok is false where the runtime cannot wait on the descriptor, or the file is
// closed: pollReadableTimed answers then.
func awaitReadable(file *os.File) (readable, closed, ok bool) {
	conn, err := file.SyscallConn()
	if err != nil {
		return false, false, false
	}
	fds := []unix.PollFd{{Events: unix.POLLIN}}
	var pollErr error
	err = conn.Read(func(fd uintptr) bool {
		fds[0].Fd = int32(fd)
		fds[0].Revents = 0
		for {
			if _, pollErr = unix.Poll(fds, 0); !errors.Is(pollErr, unix.EINTR) {
				break
			}
		}
		return pollErr != nil || fds[0].Revents != 0
	})
	if err != nil || pollErr != nil {
		return false, false, false
	}
	if fds[0].Revents&unix.POLLNVAL != 0 {
		return false, true, true
	}
	return fds[0].Revents&(unix.POLLIN|unix.POLLHUP|unix.POLLERR) != 0, false, true
}

// pollReadableTimed waits up to timeoutMS for the read end of a pipe to become readable.
func pollReadableTimed(file *os.File, timeoutMS int) (readable, closed bool) {
	conn, err := file.SyscallConn()
	if err != nil {
		return false, true
	}
	fds := []unix.PollFd{{Events: unix.POLLIN}}
	var pollErr error
	if err := conn.Control(func(fd uintptr) {
		fds[0].Fd = int32(fd)
		for {
			if _, pollErr = unix.Poll(fds, timeoutMS); !errors.Is(pollErr, unix.EINTR) {
				return
			}
		}
	}); err != nil || pollErr != nil {
		return false, true
	}
	if fds[0].Revents&unix.POLLNVAL != 0 {
		return false, true
	}
	return fds[0].Revents&(unix.POLLIN|unix.POLLHUP|unix.POLLERR) != 0, false
}

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
