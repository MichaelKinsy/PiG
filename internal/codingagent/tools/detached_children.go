package tools

import (
	"os"
	"os/exec"
	"sync"

	"github.com/MichaelKinsy/PiG/internal/nodespawn"
)

// A running shell command has its own process group (a job object on Windows), so a terminal hangup does not reach it
// and it outlives a pig exit that skips orderly shutdown. Each one is tracked from its start until its wait ends, so
// such an exit can still kill it.
// Ports packages/coding-agent/src/utils/shell.ts (trackDetachedChildPid, untrackDetachedChildPid, killTrackedDetachedChildren).
var detachedChildren = newDetachedChildSet()

// detachedChildSet holds the running shell commands. Pi spawns and tracks a child in one synchronous step, so its kill
// cannot run between them; here a lock gives the same guarantee. As in Pi, the kill does not stop later starts.
type detachedChildSet struct {
	mu        sync.Mutex
	processes map[*os.Process]struct{}
}

func newDetachedChildSet() *detachedChildSet {
	return &detachedChildSet{processes: make(map[*os.Process]struct{})}
}

// start starts cmd, joins it to its process group and tracks it under the lock killAll takes, so a kill either finds
// the command or runs before it starts.
func (s *detachedChildSet) start(cmd *exec.Cmd) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := nodespawn.Start(cmd); err != nil {
		return err
	}
	attachProcessGroup(cmd.Process)
	s.processes[cmd.Process] = struct{}{}
	return nil
}

// untrack waits for a kill in progress, so the caller cannot release the process group under it.
func (s *detachedChildSet) untrack(p *os.Process) {
	s.mu.Lock()
	delete(s.processes, p)
	s.mu.Unlock()
}

// killAll kills the process group of every running shell command and forgets them.
func (s *detachedChildSet) killAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for p := range s.processes {
		_ = killProcessGroup(p)
	}
	clear(s.processes)
}

// KillTrackedDetachedChildren kills the process group of every running shell command and forgets them.
func KillTrackedDetachedChildren() {
	detachedChildren.killAll()
}
