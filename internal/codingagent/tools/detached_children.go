package tools

import (
	"os"
	"sync"
)

// A running shell command has its own process group (a job object on Windows), so a terminal hangup does not reach it
// and it outlives a pig exit that skips orderly shutdown. Each one is tracked from its start until its wait ends, so
// such an exit can still kill it.
// Ports packages/coding-agent/src/utils/shell.ts (trackDetachedChildPid, untrackDetachedChildPid, killTrackedDetachedChildren).
var detachedChildren = struct {
	sync.Mutex
	processes map[*os.Process]struct{}
}{processes: make(map[*os.Process]struct{})}

func trackDetachedChild(p *os.Process) {
	detachedChildren.Lock()
	detachedChildren.processes[p] = struct{}{}
	detachedChildren.Unlock()
}

// untrackDetachedChild waits for a kill in progress, so the caller cannot release the process group under it.
func untrackDetachedChild(p *os.Process) {
	detachedChildren.Lock()
	delete(detachedChildren.processes, p)
	detachedChildren.Unlock()
}

// KillTrackedDetachedChildren kills the process group of every running shell command and forgets them.
func KillTrackedDetachedChildren() {
	detachedChildren.Lock()
	defer detachedChildren.Unlock()
	for p := range detachedChildren.processes {
		_ = killProcessGroup(p)
	}
	clear(detachedChildren.processes)
}
