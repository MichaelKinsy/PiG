//go:build darwin

package daemon

// Ports packages/env/daemon/src/sys/unix.rs (kill_member, all_pids)

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// killMember kills member if it is still of session sid when the signal goes: a pid found earlier may name another
// process since. macOS has no pidfd, so the check is made right before the signal, a window of a few instructions.
func killMember(member, sid int) {
	if got, err := unix.Getsid(member); err == nil && got == sid {
		_ = syscall.Kill(member, syscall.SIGKILL)
	}
}

// allPids returns every process id this user may see via sysctl kern.proc.all on macOS.
func allPids() []int {
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil
	}
	pids := make([]int, 0, len(procs))
	for i := range procs {
		pids = append(pids, int(procs[i].Proc.P_pid))
	}
	return pids
}
