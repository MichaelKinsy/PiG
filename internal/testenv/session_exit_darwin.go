//go:build darwin

package testenv

import (
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

// darwinProcZombie matches SZOMB (5) in XNU <sys/proc.h>: the process has exited
// and is awaiting collection by its parent.
const darwinProcZombie = 5

// LiveSessionProcesses lists the processes of terminal session sid that have not exited.
// macOS 15's /usr/bin/pgrep has no -s option, and XNU's sysctl tree exposes no
// kern.proc.session node (sysctlnametomib fails with ENOENT, and the numeric
// KERN_PROC_SESSION subtype is absent in sysctl_allproc). Querying the process
// table via kern.proc.all and matching each non-zombie process's session ID with
// getsid(2) keeps the signature without invoking any external tool. A zombie has
// exited and writes nothing more; it stays in the process table until its parent
// reaps it, so it does not count.
func LiveSessionProcesses(sid int) ([]int, error) {
	if sid <= 0 {
		return nil, nil
	}
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, fmt.Errorf("sysctl kern.proc.all: %w", err)
	}
	var live []int
	for i := range procs {
		p := &procs[i].Proc
		if p.P_stat == darwinProcZombie {
			continue
		}
		pid := int(p.P_pid)
		if pid <= 0 {
			continue
		}
		procSID, err := unix.Getsid(pid)
		if err != nil {
			// ESRCH: process exited or turned zombie between the sysctl snapshot and getsid.
			// EPERM: caller lacks permission to inspect an unrelated user's process.
			if errors.Is(err, unix.ESRCH) || errors.Is(err, unix.EPERM) {
				continue
			}
			return nil, fmt.Errorf("getsid(%d): %w", pid, err)
		}
		if procSID == sid {
			live = append(live, pid)
		}
	}
	return live, nil
}
