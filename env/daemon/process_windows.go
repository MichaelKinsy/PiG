//go:build windows

package daemon

// Ports packages/env/daemon/src/sys/windows.rs

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/MichaelKinsy/PiG/internal/nodespawn"
)

// programAttributes is nil: a Windows program is not started in a session of its own.
func programAttributes() *syscall.SysProcAttr { return nil }

func startCommand(cmd *exec.Cmd) error { return nodespawn.Start(cmd) }

// killTree is Node's killProcessTree on Windows: `taskkill /F /T`, not waited for.
func killTree(pid int) {
	systemRoot := os.Getenv("SystemRoot")
	if systemRoot == "" {
		systemRoot = `C:\Windows`
	}
	//nolint:gosec // G204 (class a): the program is System32\taskkill.exe under SystemRoot, with fixed arguments and the pid of a program the daemon started.
	command := exec.Command(filepath.Join(systemRoot, "System32", "taskkill.exe"), "/F", "/T", "/PID", strconv.Itoa(pid))
	nodespawn.HideWindow(command, nodespawn.Ignore, nodespawn.Ignore, nodespawn.Ignore)
	if command.Start() != nil {
		return
	}
	//portlint:allow golifetime reaps taskkill; it returns when taskkill exits
	go func() { _ = command.Wait() }()
}
