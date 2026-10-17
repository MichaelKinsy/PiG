//go:build linux

package daemon

import (
	"runtime"

	"golang.org/x/sys/unix"
)

// withEmptySignalMask runs start with an empty blocked-signal mask on the calling thread and restores the thread's mask after.
// Go gives a forked child the mask of the thread that forks (runtime.syscall_runtime_BeforeFork saves it, AfterForkInChild
// restores it), so a child started inside start has none blocked: pty.rs's pre_exec sets an empty mask before exec, as libuv does.
// Known difference from pty.rs, which clears the mask in the child only: the daemon's own thread runs unblocked from the
// first call to the last, so a signal the daemon blocked and that is already pending (SIGTERM, say) is delivered to the daemon
// in that window. Go offers no pre-exec hook; closing the window takes a re-exec helper.
// Without it a daemon started with a signal blocked (a shell's `trap`, a process manager, a debugger) hands the block to every program.
func withEmptySignalMask(start func() error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	var empty, previous unix.Sigset_t
	if err := unix.PthreadSigmask(unix.SIG_SETMASK, &empty, &previous); err != nil {
		return err
	}
	defer func() { _ = unix.PthreadSigmask(unix.SIG_SETMASK, &previous, nil) }()
	return start()
}
