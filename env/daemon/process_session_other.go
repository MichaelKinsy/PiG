//go:build !windows && !linux && !darwin

package daemon

// Ports packages/env/daemon/src/sys/unix.rs (kill_member, all_pids)

import (
	"os"
	"strconv"
	"syscall"

	"golang.org/x/sys/unix"
)

func killMember(member, sid int) {
	if got, err := unix.Getsid(member); err == nil && got == sid {
		_ = syscall.Kill(member, syscall.SIGKILL)
	}
}

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
