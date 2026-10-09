package tools

import (
	"os"
	"slices"
	"sync"
)

// trackedDetachedChildren are the shells a bash tool call started in their own process group. They are killed when
// PiG shuts down on a termination signal or crashes, because a detached shell does not receive the terminal's hangup.
//
// spawning is read-locked from before a shell starts until it is tracked; KillTrackedDetachedChildren takes it for
// writing first, so the kill sees every shell that exists. Pi's single thread spawns and tracks in one turn, so its handlers never see that window.
var trackedDetachedChildren = struct {
	spawning  sync.RWMutex
	mu        sync.Mutex
	processes map[*os.Process]struct{}
}{processes: map[*os.Process]struct{}{}}

// TrackDetachedChild records a detached child process so KillTrackedDetachedChildren can kill it.
//
// Ports packages/coding-agent/src/utils/shell.ts (trackDetachedChildPid).
func TrackDetachedChild(process *os.Process) {
	trackedDetachedChildren.mu.Lock()
	defer trackedDetachedChildren.mu.Unlock()
	trackedDetachedChildren.processes[process] = struct{}{}
}

// UntrackDetachedChild forgets a detached child process that has ended.
//
// Ports packages/coding-agent/src/utils/shell.ts (untrackDetachedChildPid).
func UntrackDetachedChild(process *os.Process) {
	trackedDetachedChildren.mu.Lock()
	defer trackedDetachedChildren.mu.Unlock()
	delete(trackedDetachedChildren.processes, process)
}

// KillTrackedDetachedChildren kills the process tree of every tracked child and forgets them all. It also sends SIGTERM
// to the process group of every running MCP stdio server: Pi's stdio transport kills those groups from a
// process.once("exit") hook, which runs on every exit including the dead-terminal and crash exits, while Go's os.Exit
// runs no hooks. Every caller is an exit that skips the orderly session shutdown.
//
// Ports packages/coding-agent/src/utils/shell.ts (killTrackedDetachedChildren) and packages/mcp/src/transports/stdio.ts
// (installExitHook).
func KillTrackedDetachedChildren() {
	trackedDetachedChildren.spawning.Lock()
	trackedDetachedChildren.mu.Lock()
	processes := make([]*os.Process, 0, len(trackedDetachedChildren.processes))
	for process := range trackedDetachedChildren.processes {
		processes = append(processes, process)
	}
	clear(trackedDetachedChildren.processes)
	trackedDetachedChildren.mu.Unlock()
	trackedDetachedChildren.spawning.Unlock()
	for _, process := range processes {
		_ = killProcessGroup(process)
	}
	for _, cleanup := range exitCleanups() {
		cleanup()
	}
}

var (
	exitCleanupsMu sync.Mutex
	exitCleanupFns []func()
)

// RegisterExitCleanup adds cleanup to what [KillTrackedDetachedChildren] runs after it kills the tracked children. A package
// that owns processes of its own registers its kill here so this package does not link it (a pig_strip_mcp build links no mcp).
func RegisterExitCleanup(cleanup func()) {
	exitCleanupsMu.Lock()
	exitCleanupFns = append(exitCleanupFns, cleanup)
	exitCleanupsMu.Unlock()
}

func exitCleanups() []func() {
	exitCleanupsMu.Lock()
	defer exitCleanupsMu.Unlock()
	return slices.Clone(exitCleanupFns)
}
