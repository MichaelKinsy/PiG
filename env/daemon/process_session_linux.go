//go:build linux

package daemon

// Ports packages/env/daemon/src/sys/unix.rs (kill_member, all_pids)

import (
	"os"
	"strconv"
	"syscall"

	"golang.org/x/sys/unix"
)

// killMember kills member if it is still of session sid when the signal goes: a pid found earlier may name another
// process since. On Linux a pidfd (pidfd_open, Linux 5.3) holds the process while it is checked; without one (an older
// kernel, Android, whose seccomp may forbid the call) the check is made right before the signal, a window of a few
// instructions.
func killMember(member, sid int) {
	fd, err := unix.PidfdOpen(member, 0)
	if err == nil {
		// The descriptor names the process member named when it was opened: if that one exited and the pid was reused
		// before the check, the signal goes to the gone process and does nothing.
		if got, err := unix.Getsid(member); err == nil && got == sid {
			_ = unix.PidfdSendSignal(fd, syscall.SIGKILL, nil, 0)
		}
		_ = unix.Close(fd)
		return
	}
	if got, err := unix.Getsid(member); err == nil && got == sid {
		_ = syscall.Kill(member, syscall.SIGKILL)
	}
}

// allPids returns every process id this user may see: /proc on Linux and Android.
func allPids() []int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	pids := make([]int, 0, len(entries))
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err == nil {
			pids = append(pids, pid)
		}
	}
	return pids
}
