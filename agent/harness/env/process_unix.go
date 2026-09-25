//go:build !windows

package env

import (
	"context"
	"syscall"
)

const isWindows = false

// detachedProcessAttributes starts the shell as a process-group leader so
// killProcessTree can signal every descendant.
func detachedProcessAttributes() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}

func hiddenProcessAttributes() *syscall.SysProcAttr { return nil }

// killProcessTree sends SIGKILL to the process group, falling back to the
// process itself.
var killProcessTree = func(pid int) error {
	if err := syscall.Kill(-pid, syscall.SIGKILL); err == nil {
		return nil
	}
	return syscall.Kill(pid, syscall.SIGKILL)
}

func platformShellConfig(ctx context.Context) (shellConfig, error) {
	if pathExists("/bin/bash") {
		return getBashShellConfig("/bin/bash"), nil
	}
	if bash := findBashOnPath(ctx); bash != "" {
		return getBashShellConfig(bash), nil
	}
	return shellConfig{shell: "sh", args: []string{"-c"}}, nil
}

// signalExitCode maps a signal-terminated process to 128 + signal number.
func signalExitCode(status syscall.WaitStatus) (int, bool) {
	if status.Signaled() {
		return 128 + int(status.Signal()), true
	}
	return 0, false
}
