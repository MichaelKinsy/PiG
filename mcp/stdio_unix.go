//go:build unix

package mcp

import (
	"context"
	"os/exec"
	"sync"
	"syscall"
)

// Ports packages/mcp/src/transports/stdio.ts (USE_PROCESS_GROUPS, killProcessTree, the exit hook that ends live server groups)

var (
	liveMu     sync.Mutex
	liveGroups = map[int]struct{}{}
)

func bgContext() context.Context { return context.Background() }

// setStdioProcAttr starts the server as the leader of a new session, as Pi's stdio transport spawns it with `detached: USE_PROCESS_GROUPS` (stdio.ts), which
// Node starts with setsid(2). The server leads its own process group, so closing the transport can terminate the server's children too, and it has no
// controlling terminal: a server or a child of it cannot open /dev/tty and take the terminal's foreground group from PiG.
func setStdioProcAttr(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setsid = true
}

func trackProcessGroup(pid int) {
	liveMu.Lock()
	liveGroups[pid] = struct{}{}
	liveMu.Unlock()
}

func untrackProcessGroup(pid int) {
	liveMu.Lock()
	delete(liveGroups, pid)
	liveMu.Unlock()
}

// KillLiveProcessGroups sends SIGTERM to the process group of every stdio
// server still running. A host calls it as it exits without closing its
// transports (upstream's process "exit" hook).
func KillLiveProcessGroups() {
	liveMu.Lock()
	pids := make([]int, 0, len(liveGroups))
	for pid := range liveGroups {
		pids = append(pids, pid)
	}
	liveMu.Unlock()
	for _, pid := range pids {
		_ = syscall.Kill(-pid, syscall.SIGTERM)
	}
}

// killProcessTree signals the whole group, so wrappers like `npx` or `uvx` do
// not leave the server behind, and falls back to the direct child.
func killProcessTree(cmd *exec.Cmd, _ <-chan struct{}, kill bool) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	signal := syscall.SIGTERM
	if kill {
		signal = syscall.SIGKILL
	}
	if err := syscall.Kill(-cmd.Process.Pid, signal); err == nil {
		return
	}
	_ = cmd.Process.Signal(signal)
}
