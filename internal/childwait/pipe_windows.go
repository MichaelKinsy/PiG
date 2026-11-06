//go:build windows

package childwait

import (
	"errors"
	"os"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// pollSupported reports that PeekNamedPipe answers for the read end of a pipe.
const pollSupported = true

// procPeekNamedPipe is kernel32's PeekNamedPipe, which golang.org/x/sys/windows does not wrap.
var procPeekNamedPipe = windows.NewLazySystemDLL("kernel32.dll").NewProc("PeekNamedPipe")

// peekPipe asks how many bytes wait in the read end of a pipe without taking any. ended reports that every write end is closed
// (ERROR_BROKEN_PIPE), and closed that the handle is gone, as it is once Wait closed it.
func peekPipe(file *os.File) (avail uint32, ended, closed bool) {
	conn, err := file.SyscallConn()
	if err != nil {
		return 0, false, true
	}
	var peekErr error
	if err := conn.Control(func(fd uintptr) {
		ok, _, callErr := procPeekNamedPipe.Call(fd, 0, 0, 0, uintptr(unsafe.Pointer(&avail)), 0)
		if ok == 0 {
			peekErr = callErr
		}
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
//
// This is a 1 ms poll because no wait object exists for it. os.Pipe hands out synchronous handles, and the I/O on a synchronous handle is serialized on
// its file object: a zero-byte ReadFile (libuv's way to wait for data) or any blocking read pending on the earlier pipe would hold up the PeekNamedPipe
// that a later pipe's reader makes on it (holdsOutput), and a pending read could not be told apart from a handle Wait closed. An event wait needs the pipe
// created with FILE_FLAG_OVERLAPPED, which changes what Start is given; see the 0.7.1 WinWaits hand-off.
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
