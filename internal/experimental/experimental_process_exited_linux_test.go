package experimental

import (
	"errors"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

// pfExiting is PF_EXITING from <linux/sched.h>: the task has entered do_exit and runs no more user code.
const pfExiting = 0x4

// processExited reports whether /proc shows pid as exited: inside kernel exit teardown, a zombie awaiting its reaper, or already reaped. The kernel closes the worker's control socket during exit teardown before the task becomes a zombie.
func processExited(t *testing.T, pid int) bool {
	t.Helper()
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	// A reaper that collects the worker during the read yields ESRCH instead of ENOENT.
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ESRCH) {
		return true
	}
	if err != nil {
		t.Fatalf("read worker %d state: %v", pid, err)
	}
	// Fields after the parenthesized command name, which may itself contain ")": state, ppid, pgrp, session, tty_nr, tpgid, flags.
	end := strings.LastIndexByte(string(stat), ')')
	fields := strings.Fields(string(stat[end+1:]))
	if end < 0 || len(fields) < 7 {
		t.Fatalf("worker %d state is malformed: %q", pid, stat)
	}
	flags, err := strconv.ParseUint(fields[6], 10, 64)
	if err != nil {
		t.Fatalf("worker %d flags are malformed: %q", pid, stat)
	}
	return fields[0] == "Z" || fields[0] == "X" || flags&pfExiting != 0
}
