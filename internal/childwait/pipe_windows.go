//go:build windows

package childwait

import (
	"errors"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

// pollSupported reports that PeekNamedPipe answers for the read end of a pipe.
const pollSupported = true

// peekPipe asks how many bytes wait in the read end of a pipe without taking any. ended reports that every write end is closed
// (ERROR_BROKEN_PIPE), and closed that the handle is gone, as it is once Wait closed it.
func peekPipe(file *os.File) (avail uint32, ended, closed bool) {
	conn, err := file.SyscallConn()
	if err != nil {
		return 0, false, true
	}
	var peekErr error
	if err := conn.Control(func(fd uintptr) {
		peekErr = windows.PeekNamedPipe(windows.Handle(fd), nil, 0, nil, &avail, nil)
	}); err != nil {
		return 0, false, true
	}
	switch {
	case peekErr == nil:
		return avail, false, false
	case errors.Is(peekErr, windows.ERROR_BROKEN_PIPE):
		return avail, true, false
	}
	return 0, false, true
}

// pollReadable waits up to timeoutMS for the read end of a pipe to hold bytes or to end. closed is true when the handle is gone.
func pollReadable(file *os.File, timeoutMS int) (readable, closed bool) {
	deadline := time.Now().Add(time.Duration(timeoutMS) * time.Millisecond)
	for {
		avail, ended, gone := peekPipe(file)
		switch {
		case gone:
			return false, true
		case avail > 0 || ended:
			return true, false
		case !time.Now().Before(deadline):
			return false, false
		}
		time.Sleep(time.Millisecond)
	}
}

// pollPending reports whether the read end of a pipe holds bytes.
func pollPending(file *os.File) bool {
	avail, _, _ := peekPipe(file)
	return avail > 0
}
