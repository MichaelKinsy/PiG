package experimental

import (
	"errors"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

// From <sys/proc.h>: SZOMB marks an exited process awaiting its parent's wait, and P_WEXIT marks a process inside kernel exit teardown.
const (
	sZomb  = 5
	pWexit = 0x00002000
)

// processExited reports whether the kernel process table shows pid as exited: inside kernel exit teardown, a zombie awaiting its reaper, or already reaped.
func processExited(t *testing.T, pid int) bool {
	t.Helper()
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		if errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
			return true
		}
		t.Fatalf("read worker %d state: %v", pid, err)
	}
	return info.Proc.P_pid == int32(pid) && (info.Proc.P_stat == sZomb || info.Proc.P_flag&pWexit != 0)
}
