//go:build darwin

package daemon

import (
	"runtime"
	"syscall"
	"unsafe"
)

// sigSetMask is SIG_SETMASK of <sys/signal.h>.
const sigSetMask = 3

// withEmptySignalMask runs start with an empty blocked-signal mask on the calling thread and restores the thread's mask after.
// Go gives a forked child the mask of the thread that forks, so a child started inside start has none blocked: pty.rs's pre_exec
// sets an empty mask before exec, as libuv does. Known difference: the daemon's own thread runs unblocked from the first call to
// the last, so a signal it blocked and that is already pending is delivered to it in that window; closing it takes a re-exec helper. A darwin sigset_t is one uint32 and the sigprocmask system call takes pointers.
func withEmptySignalMask(start func() error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	var empty, previous uint32
	if _, _, errno := syscall.RawSyscall(syscall.SYS_SIGPROCMASK, sigSetMask, uintptr(unsafe.Pointer(&empty)), uintptr(unsafe.Pointer(&previous))); errno != 0 { //nolint:gosec // G103: sigprocmask reads and writes the caller's signal-set words through the syscall arguments; the daemon is built without cgo
		return errno
	}
	defer func() {
		_, _, _ = syscall.RawSyscall(syscall.SYS_SIGPROCMASK, sigSetMask, uintptr(unsafe.Pointer(&previous)), 0) //nolint:gosec // G103: sigprocmask restores the signal-set word through the syscall argument; the daemon is built without cgo
	}()
	return start()
}
