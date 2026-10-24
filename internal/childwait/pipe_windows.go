//go:build windows

package childwait

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// pollPipe asks Windows about the read end of an anonymous pipe without taking a byte from it (PeekNamedPipe). A pipe whose write ends are all closed
// and whose bytes are all read fails with ERROR_BROKEN_PIPE: hungUp. While bytes wait in the pipe, whether or not a write end is open, they are unread:
// the readers read them, and the end of the stream follows. known is false when Windows did not answer.
func pollPipe(file *os.File) (hungUp, unread, known bool) {
	conn, err := file.SyscallConn()
	if err != nil {
		return false, false, false
	}
	var (
		available uint32
		peekErr   error
	)
	if err := conn.Control(func(fd uintptr) {
		peekErr = windows.PeekNamedPipe(windows.Handle(fd), nil, 0, nil, &available, nil)
	}); err != nil {
		return false, false, false
	}
	switch {
	case errors.Is(peekErr, windows.ERROR_BROKEN_PIPE):
		return true, false, true
	case peekErr != nil:
		return false, false, false
	}
	return false, available > 0, true
}
