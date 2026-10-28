package nodefs

import (
	"os"
	"runtime"
	"syscall"
	"unsafe"
)

// namesFIFO reports whether name, with symbolic links followed, is a FIFO. It is one stat into the stack, so opening a file that is
// not a FIFO costs no allocation; a path that cannot be stat'ed is left to the open to report.
func namesFIFO(name string) bool {
	var stat syscall.Stat_t
	return syscall.Stat(name, &stat) == nil && stat.Mode&syscall.S_IFMT == syscall.S_IFIFO
}

// pthreadSigmaskBlock is pthread_sigmask's SIG_BLOCK.
const pthreadSigmaskBlock = 1

// quietOpen opens name on a thread that blocks every signal, as libuv's worker threads are never the ones a signal is delivered
// to.
//
// A signal that ends a blocking open of a FIFO on macOS makes the kernel (fifo_open in XNU's bsd/miscfs/fifofs/fifo_vnops.c)
// leave through fifo_close_internal, which counts the opener out and shuts the FIFO's send (or receive) side while the other
// end, whose open completed because it saw the opener, goes on: its write fails with EPIPE (the bytes are lost), or its read
// returns end of file at once while the retried open waits for a counterpart that is gone. Retrying the open on EINTR, as os
// does, cannot undo that; the signal must not arrive.
//
// The thread is the goroutine's own, locked and never unlocked: when the goroutine returns, the runtime retires the thread with
// its mask instead of handing it to other goroutines. Signals sent to the process go to the threads that do not block them.
func quietOpen(name string, flag int, perm os.FileMode) (*os.File, error) {
	type opened struct {
		file *os.File
		err  error
	}
	done := make(chan opened, 1)
	go func() {
		runtime.LockOSThread()
		all := ^uint32(0)                                                                                                                     // macOS's sigset_t is one word
		if _, _, errno := syscall.Syscall(syscall.SYS___PTHREAD_SIGMASK, pthreadSigmaskBlock, uintptr(unsafe.Pointer(&all)), 0); errno != 0 { //nolint:gosec // G103: blocks signals on this thread for the open call; the pointer is to a local sigset_t that the kernel reads once during the call and nothing keeps
			done <- opened{err: &os.PathError{Op: "open", Path: name, Err: errno}}
			return
		}
		file, err := os.OpenFile(name, flag, perm) //nolint:gosec // G304: the path names an output or state file that the user or PiG's own state directory designates; the CLI runs as the user with no sandbox, as Pi does, so writing it grants nothing the user lacks
		done <- opened{file, err}
	}()
	result := <-done
	return result.file, result.err
}
