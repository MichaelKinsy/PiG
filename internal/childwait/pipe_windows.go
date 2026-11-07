//go:build windows

package childwait

import (
	"errors"
	"os"
	"runtime"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// pollSupported reports that PeekNamedPipe answers for the read end of a pipe.
const pollSupported = true

// procPeekNamedPipe is kernel32's PeekNamedPipe, which golang.org/x/sys/windows does not wrap.
var procPeekNamedPipe = windows.NewLazySystemDLL("kernel32.dll").NewProc("PeekNamedPipe")

// fileModeInformation is FILE_MODE_INFORMATION; synchronousIO is FILE_SYNCHRONOUS_IO_ALERT | FILE_SYNCHRONOUS_IO_NONALERT.
type fileModeInformation struct{ mode uint32 }

const (
	fileModeInformationClass = 16
	synchronousIO            = windows.FILE_SYNCHRONOUS_IO_ALERT | windows.FILE_SYNCHRONOUS_IO_NONALERT
)

// overlapped reports whether the handle was opened with FILE_FLAG_OVERLAPPED, as the read end of NewPipe is. A synchronous handle (os.Pipe's) cannot be
// waited on without holding up every other call on it.
func overlapped(handle windows.Handle) bool {
	var info fileModeInformation
	var status windows.IO_STATUS_BLOCK
	if err := windows.NtQueryInformationFile(handle, &status, (*byte)(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), fileModeInformationClass); err != nil { //nolint:gosec // G103: the API fills the FILE_MODE_INFORMATION layout fileModeInformation mirrors, and x/sys takes that buffer as *byte.
		return false
	}
	return info.mode&synchronousIO == 0
}

// isPipe reports whether the handle is a pipe, the only kind PeekNamedPipe answers for. A handle that cannot be asked is not taken for one.
func isPipe(handle windows.Handle) bool {
	kind, err := windows.GetFileType(handle)
	return err == nil && kind == windows.FILE_TYPE_PIPE
}

// peekHandle asks how many bytes wait in the read end of a pipe without taking any. ended reports that every write end is closed
// (ERROR_BROKEN_PIPE).
func peekHandle(handle windows.Handle) (avail uint32, ended bool, err error) {
	ok, _, callErr := procPeekNamedPipe.Call(uintptr(handle), 0, 0, 0, uintptr(unsafe.Pointer(&avail)), 0) //nolint:gosec // G103: PeekNamedPipe fills the available count, and x/sys has no wrapper for it.
	switch {
	case ok != 0:
		return avail, false, nil
	case errors.Is(callErr, windows.ERROR_BROKEN_PIPE):
		return avail, true, nil
	}
	return 0, false, callErr
}

// peekPipe asks how many bytes wait in the read end of a pipe without taking any. ended reports that every write end is closed
// (ERROR_BROKEN_PIPE), and closed that the handle is gone, as it is once Wait closed it.
func peekPipe(file *os.File) (avail uint32, ended, closed bool) {
	conn, err := file.SyscallConn()
	if err != nil {
		return 0, false, true
	}
	var peekErr error
	if err := conn.Control(func(fd uintptr) {
		avail, ended, peekErr = peekHandle(windows.Handle(fd))
	}); err != nil {
		return 0, false, true
	}
	if peekErr != nil {
		return 0, false, true
	}
	return avail, ended, false
}

// pollReadable waits up to timeoutMS for the read end of a pipe to hold bytes or to end. closed is true when the handle is gone.
//
// On the overlapped read end that NewPipe makes it waits on an event: a zero-byte ReadFile completes when the pipe holds data or its last write end
// closes (libuv waits the same way). Any other handle (os.Pipe's, synchronous) cannot be waited on without holding up the peek of a later pipe's reader, so
// it is polled every millisecond.
//
// timeoutMS must be finite, and it is what bounds the release of the file. Closing the file cancels a read that has been issued (poll.FD.Close calls
// CancelIoEx), but a Close that lands between Control's reference on the handle and the ReadFile cancels nothing, and then waits in Close, inside the
// caller's Control, for this wait to end by itself: for the timeout, or for data or the end of the pipe. With an infinite timeout that Close could hang.
// The caller (awaitTurn) asks again after a timeout and learns then that the file is gone.
func pollReadable(file *os.File, timeoutMS int) (readable, closed bool) {
	conn, err := file.SyscallConn()
	if err != nil {
		return false, true
	}
	if err := conn.Control(func(fd uintptr) {
		readable, closed = waitReadable(windows.Handle(fd), timeoutMS)
	}); err != nil {
		return false, true
	}
	return readable, closed
}

// readOp is the zero-byte read of waitReadable: the OVERLAPPED the kernel fills in, and the count it reports.
type readOp struct {
	overlapped windows.Overlapped
	n          uint32
}

func waitReadable(handle windows.Handle, timeoutMS int) (readable, closed bool) {
	avail, ended, err := peekHandle(handle)
	switch {
	case err != nil:
		if !isPipe(handle) {
			// PeekNamedPipe answers only for pipes. A file or a console has no pipe to ask, so the reader goes straight to its read, as it does where no
			// poll answers at all (pipe_other.go).
			return true, false
		}
		return false, true
	case avail > 0 || ended:
		return true, false
	}
	if !overlapped(handle) {
		return pollSleeping(handle, timeoutMS)
	}
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return pollSleeping(handle, timeoutMS)
	}
	defer func() { _ = windows.CloseHandle(event) }()
	// The low-order bit of hEvent keeps this completion off the I/O completion port the Go runtime has the handle on, which would take it for one of its own.
	// The kernel owns the OVERLAPPED from ReadFile until the read is over, and writes its status into it then: it must not be on a goroutine stack, which the
	// runtime may move or shrink meanwhile. It is allocated on the heap and pinned for the call, as internal/poll does for its own.
	op := &readOp{}
	op.overlapped.HEvent = event | 1
	var pinner runtime.Pinner
	pinner.Pin(op)
	defer pinner.Unpin()
	err = windows.ReadFile(handle, nil, &op.n, &op.overlapped)
	if errors.Is(err, windows.ERROR_IO_PENDING) {
		wait := uint32(max(timeoutMS, 0))
		if status, _ := windows.WaitForSingleObject(event, wait); status != windows.WAIT_OBJECT_0 {
			// Timed out: end the read, and wait for it to be over, because the kernel owns op until then.
			_ = windows.CancelIoEx(handle, &op.overlapped)
		}
		err = windows.GetOverlappedResult(handle, &op.overlapped, &op.n, true)
	}
	switch {
	case err == nil:
	case errors.Is(err, windows.ERROR_BROKEN_PIPE):
		return true, false
	default:
		// Timed out, or closing the file cancelled the read: the caller asks again, and learns there whether the file is gone.
		return false, false
	}
	avail, ended, err = peekHandle(handle)
	switch {
	case err != nil:
		return false, true
	case avail > 0 || ended:
		return true, false
	}
	// A zero-byte read that completed with nothing to read (a zero-length message): do not spin on it.
	time.Sleep(time.Millisecond)
	return false, false
}

// pollSleeping polls a handle that cannot be waited on.
func pollSleeping(handle windows.Handle, timeoutMS int) (readable, closed bool) {
	deadline := time.Now().Add(time.Duration(timeoutMS) * time.Millisecond)
	for {
		avail, ended, err := peekHandle(handle)
		switch {
		case err != nil:
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
