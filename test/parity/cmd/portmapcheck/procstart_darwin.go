//go:build darwin

package main

import (
	"errors"
	"strconv"

	"golang.org/x/sys/unix"
)

// processStart is the start time of process pid. A zombie is gone.
func processStart(pid int) (string, error) {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if errors.Is(err, unix.ESRCH) {
		return "", errProcessGone
	}
	if err != nil {
		return "", err
	}
	if int(kp.Proc.P_pid) != pid || kp.Proc.P_stat == 5 { // 5 is SZOMB
		return "", errProcessGone
	}
	tv := kp.Proc.P_starttime
	return strconv.FormatInt(int64(tv.Sec), 10) + "." + strconv.FormatInt(int64(tv.Usec), 10), nil
}

// processScope: a pid means the same process to every user of the machine.
func processScope() string { return "" }
