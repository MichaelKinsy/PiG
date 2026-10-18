//go:build windows

package host

// Ports packages/chord/src/manager.ts: the Windows half of forkDriver and the process IPC channel (Node IPC framing over a named pipe).

import (
	"errors"
	"io"
	"net"
	"runtime"
	"strconv"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// An OS pipe handle Go did not open, read and written as Node's IPC channel needs: the handle behind a CRT file
// descriptor (Node's NODE_CHANNEL_FD). libuv creates that end by CreateFile, overlapped for an IPC pipe in current
// releases and synchronous in older ones and for ordinary stdio pipes, and Go cannot adopt either as a net.Conn. A
// synchronous handle serializes its I/O, so a Read pending on it would hold up every Write. libuv's cure
// (uv_pipe_zero_readfile_thread_proc, uv__pipe_interrupt_read) is this one: wait for data with a zero-byte ReadFile on
// a thread of its own, and cancel that wait (CancelSynchronousIo) whenever something is to be written.

var (
	kernel32                = windows.NewLazySystemDLL("kernel32.dll")
	procCancelSynchronousIo = kernel32.NewProc("CancelSynchronousIo")
	procPeekNamedPipe       = kernel32.NewProc("PeekNamedPipe")
	procReadFile            = kernel32.NewProc("ReadFile")
	procCreateProcessW      = kernel32.NewProc("CreateProcessW")
	// msvcrt's descriptor table is filled from the process's STARTUPINFO when the DLL loads: the CRT's own startup
	// reads the descriptors a parent passed in lpReserved2, as it does for a C child of Node.
	msvcrt          = windows.NewLazySystemDLL("msvcrt.dll")
	procGetOSHandle = msvcrt.NewProc("_get_osfhandle")
)

// osHandle is _get_osfhandle(fd).
func osHandle(fd int) (windows.Handle, error) {
	if err := procGetOSHandle.Find(); err != nil {
		return 0, err
	}
	r, _, _ := procGetOSHandle.Call(uintptr(fd))
	handle := windows.Handle(r)
	// -1 is INVALID_HANDLE_VALUE (EBADF); -2 is a descriptor with no handle (a closed standard stream).
	if handle == windows.InvalidHandle || handle == ^windows.Handle(1) {
		return 0, errors.New("file descriptor " + strconv.Itoa(fd) + " is not open")
	}
	return handle, nil
}

// fileModeInformation is FILE_MODE_INFORMATION; synchronousIO is FILE_SYNCHRONOUS_IO_ALERT | FILE_SYNCHRONOUS_IO_NONALERT.
type fileModeInformation struct{ mode uint32 }

const (
	fileModeInformationClass = 16
	synchronousIO            = windows.FILE_SYNCHRONOUS_IO_ALERT | windows.FILE_SYNCHRONOUS_IO_NONALERT
)

// isSynchronous reports whether handle was opened without FILE_FLAG_OVERLAPPED (libuv: UV_HANDLE_NON_OVERLAPPED_PIPE).
func isSynchronous(handle windows.Handle) (bool, error) {
	var info fileModeInformation
	var status windows.IO_STATUS_BLOCK
	if err := windows.NtQueryInformationFile(handle, &status, (*byte)(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), fileModeInformationClass); err != nil { //nolint:gosec // G103: the API fills the FILE_MODE_INFORMATION layout fileModeInformation mirrors, and x/sys takes that buffer as *byte.
		return false, err
	}
	return info.mode&synchronousIO != 0, nil
}

// adoptPipe is uv_pipe_open: a CRT descriptor that is a pipe, as a connection.
func adoptPipe(fd int) (io.ReadWriteCloser, error) {
	handle, err := osHandle(fd)
	if err != nil {
		return nil, err
	}
	if kind, err := windows.GetFileType(handle); err != nil || kind != windows.FILE_TYPE_PIPE {
		return nil, errors.New("file descriptor " + strconv.Itoa(fd) + " is not a pipe")
	}
	synchronous, err := isSynchronous(handle)
	if err != nil {
		return nil, err
	}
	return newHandlePipe(handle, synchronous), nil
}

type handlePipe struct {
	handle      windows.Handle
	synchronous bool

	// writeMu admits one writer at a time.
	writeMu sync.Mutex

	mu   sync.Mutex
	wake *sync.Cond
	// closed: Close ran; the handle is closed once no operation is left.
	closed   bool
	inflight int
	// writing: a Write holds the handle, and a reader waits.
	writing bool
	// reader and writer are the threads of a synchronous Read waiting for data and of a synchronous Write, which
	// Close and Write cancel; readerGone is closed when that Read leaves its wait.
	reader     windows.Handle
	readerGone chan struct{}
	writer     windows.Handle
}

func newHandlePipe(handle windows.Handle, synchronous bool) *handlePipe {
	pipe := &handlePipe{handle: handle, synchronous: synchronous}
	pipe.wake = sync.NewCond(&pipe.mu)
	return pipe
}

// begin registers an operation; it fails once the pipe is closed.
func (pipe *handlePipe) begin() error {
	pipe.mu.Lock()
	defer pipe.mu.Unlock()
	if pipe.closed {
		return net.ErrClosed
	}
	pipe.inflight++
	return nil
}

func (pipe *handlePipe) end() {
	pipe.mu.Lock()
	pipe.inflight--
	pipe.wake.Broadcast()
	pipe.mu.Unlock()
}

func (pipe *handlePipe) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if err := pipe.begin(); err != nil {
		return 0, err
	}
	defer pipe.end()
	if pipe.synchronous {
		return pipe.readSynchronous(p)
	}
	return pipe.overlappedIO(p, windows.ReadFile)
}

func (pipe *handlePipe) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if err := pipe.begin(); err != nil {
		return 0, err
	}
	defer pipe.end()
	if pipe.synchronous {
		return pipe.writeSynchronous(p)
	}
	written := 0
	for written < len(p) {
		n, err := pipe.overlappedIO(p[written:], windows.WriteFile)
		written += n
		if err != nil {
			return written, err
		}
	}
	return written, nil
}

// readEnded maps the errors that end a pipe's stream to io.EOF.
func readEnded(err error) bool {
	return errors.Is(err, windows.ERROR_BROKEN_PIPE) || errors.Is(err, windows.ERROR_PIPE_NOT_CONNECTED) ||
		errors.Is(err, windows.ERROR_NO_DATA) || errors.Is(err, windows.ERROR_HANDLE_EOF)
}

// overlappedIO is one ReadFile or WriteFile on an overlapped handle, waited for.
func (pipe *handlePipe) overlappedIO(p []byte, operation func(windows.Handle, []byte, *uint32, *windows.Overlapped) error) (int, error) {
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = windows.CloseHandle(event) }()
	overlapped := windows.Overlapped{HEvent: event}
	var n uint32
	err = operation(pipe.handle, p, &n, &overlapped)
	if errors.Is(err, windows.ERROR_IO_PENDING) {
		err = windows.GetOverlappedResult(pipe.handle, &overlapped, &n, true)
	}
	switch {
	case err == nil:
		return int(n), nil
	case errors.Is(err, windows.ERROR_OPERATION_ABORTED):
		return int(n), net.ErrClosed
	case readEnded(err):
		return int(n), io.EOF
	}
	return int(n), err
}

// currentThread is a handle to the calling thread, which CancelSynchronousIo can address from another.
func currentThread() (windows.Handle, error) {
	process, pseudo := windows.CurrentProcess(), windows.CurrentThread()
	var thread windows.Handle
	if err := windows.DuplicateHandle(process, pseudo, process, &thread, 0, false, windows.DUPLICATE_SAME_ACCESS); err != nil {
		return 0, err
	}
	return thread, nil
}

func cancelSynchronousIO(thread windows.Handle) {
	_, _, _ = procCancelSynchronousIo.Call(uintptr(thread))
}

var zeroByte [1]byte

// readSynchronous waits for data with a zero-byte read, which a Write may cancel, then reads what is there.
func (pipe *handlePipe) readSynchronous(p []byte) (int, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	thread, err := currentThread()
	if err != nil {
		return 0, err
	}
	defer func() { _ = windows.CloseHandle(thread) }()
	for {
		pipe.mu.Lock()
		for pipe.writing && !pipe.closed {
			pipe.wake.Wait()
		}
		if pipe.closed {
			pipe.mu.Unlock()
			return 0, net.ErrClosed
		}
		pipe.reader, pipe.readerGone = thread, make(chan struct{})
		gone := pipe.readerGone
		pipe.mu.Unlock()

		n, err := pipe.readAvailable(p)

		pipe.mu.Lock()
		pipe.reader, pipe.readerGone = 0, nil
		close(gone)
		pipe.wake.Broadcast()
		closed := pipe.closed
		pipe.mu.Unlock()
		switch {
		case err == nil && n > 0:
			return n, nil
		case err == nil, errors.Is(err, windows.ERROR_OPERATION_ABORTED):
			if closed {
				return 0, net.ErrClosed
			}
			if err == nil {
				// Woken with nothing to read: do not spin.
				time.Sleep(time.Millisecond)
			}
		case readEnded(err):
			return 0, io.EOF
		default:
			return 0, err
		}
	}
}

// readAvailable blocks until the pipe holds data, then reads what is there, up to len(p) (libuv: ReadFile of zero
// bytes, PeekNamedPipe, ReadFile). It returns ERROR_OPERATION_ABORTED when a Write or Close cancelled the wait.
func (pipe *handlePipe) readAvailable(p []byte) (int, error) {
	var n uint32
	if r, _, errno := procReadFile.Call(uintptr(pipe.handle), uintptr(unsafe.Pointer(&zeroByte[0])), 0, uintptr(unsafe.Pointer(&n)), 0); r == 0 { //nolint:gosec // G103: a zero-byte ReadFile is given a valid buffer pointer and the count variable it fills, and x/sys has no zero-length wrapper that waits.
		return 0, errno
	}
	var available uint32
	if r, _, errno := procPeekNamedPipe.Call(uintptr(pipe.handle), 0, 0, 0, uintptr(unsafe.Pointer(&available)), 0); r == 0 { //nolint:gosec // G103: PeekNamedPipe fills the available count, and x/sys has no wrapper for it.
		return 0, errno
	}
	if available == 0 {
		return 0, nil
	}
	if uint64(len(p)) > uint64(available) {
		p = p[:available]
	}
	if err := windows.ReadFile(pipe.handle, p, &n, nil); err != nil {
		return 0, err
	}
	return int(n), nil
}

// writeSynchronous cancels the pending wait for data, which would otherwise hold the handle, and writes with it held.
func (pipe *handlePipe) writeSynchronous(p []byte) (int, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	thread, err := currentThread()
	if err != nil {
		return 0, err
	}
	defer func() { _ = windows.CloseHandle(thread) }()
	pipe.writeMu.Lock()
	defer pipe.writeMu.Unlock()
	pipe.mu.Lock()
	pipe.writing, pipe.writer = true, thread
	for pipe.reader != 0 {
		// The wait may not have reached its ReadFile yet, so cancel again until it has left.
		cancelSynchronousIO(pipe.reader)
		gone := pipe.readerGone
		pipe.mu.Unlock()
		select {
		case <-gone:
		case <-time.After(time.Millisecond):
		}
		pipe.mu.Lock()
	}
	pipe.mu.Unlock()
	written := 0
	var failure error
	for written < len(p) {
		var n uint32
		if err := windows.WriteFile(pipe.handle, p[written:], &n, nil); err != nil {
			failure = err
			if errors.Is(err, windows.ERROR_OPERATION_ABORTED) {
				failure = net.ErrClosed
			} else if readEnded(err) {
				failure = io.ErrClosedPipe
			}
			break
		}
		written += int(n)
	}
	pipe.mu.Lock()
	pipe.writing, pipe.writer = false, 0
	pipe.wake.Broadcast()
	pipe.mu.Unlock()
	return written, failure
}

// Close cancels what is pending and closes the handle once nothing uses it.
func (pipe *handlePipe) Close() error {
	pipe.mu.Lock()
	if pipe.closed {
		pipe.mu.Unlock()
		return nil
	}
	pipe.closed = true
	pipe.wake.Broadcast()
	for pipe.inflight > 0 {
		if pipe.synchronous {
			if pipe.reader != 0 {
				cancelSynchronousIO(pipe.reader)
			}
			if pipe.writer != 0 {
				cancelSynchronousIO(pipe.writer)
			}
		} else {
			_ = windows.CancelIoEx(pipe.handle, nil)
		}
		pipe.mu.Unlock()
		time.Sleep(time.Millisecond)
		pipe.mu.Lock()
	}
	pipe.mu.Unlock()
	return windows.CloseHandle(pipe.handle)
}
