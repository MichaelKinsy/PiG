//go:build windows

package mcp

import (
	"context"
	"os/exec"
	"strconv"

	"github.com/MichaelKinsy/PiG/internal/nodespawn"
)

func bgContext() context.Context { return context.Background() }

// setStdioProcAttr keeps the attributes crossspawn set: they hold the cmd.exe
// command line that runs a .cmd shim or a command PATH does not resolve.
func setStdioProcAttr(*exec.Cmd) {}

func trackProcessGroup(int)   {}
func untrackProcessGroup(int) {}

// KillLiveProcessGroups does nothing on Windows: servers are not started in
// process groups there.
func KillLiveProcessGroups() {}

// killProcessTree ends the server and its children. Windows has no graceful
// signals, and cross-spawn runs `.cmd` shims through cmd.exe; killing only
// that would leave the server running. A server that exited is left alone
// (upstream: `if (child.exitCode !== null) return`); exited closes after
// cmd.Wait, whose ProcessState another goroutine must not read meanwhile.
func killProcessTree(cmd *exec.Cmd, exited <-chan struct{}, _ bool) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	select {
	case <-exited:
		return
	default:
	}
	kill := exec.Command("taskkill", "/pid", strconv.Itoa(cmd.Process.Pid), "/T", "/F")
	nodespawn.HideWindow(kill, nodespawn.Ignore, nodespawn.Ignore, nodespawn.Ignore)
	_ = kill.Start()
	go func() { _ = kill.Wait() }()
}
