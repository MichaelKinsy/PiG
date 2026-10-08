package tools

import (
	"errors"
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

// errPigExiting is the error of a shell command started after the exit cleanup killed the running ones.
var errPigExiting = errors.New("pig is exiting")

// detachedChildSet holds the running shell commands. Pi starts and tracks a child in one synchronous step and exits in
// the same step as the kill, so no child can start between them; here the lock gives the same guarantee.
type detachedChildSet struct {
	mu        sync.Mutex
	processes map[*os.Process]struct{}
	killed    bool
}

func newDetachedChildSet() *detachedChildSet {
	return &detachedChildSet{processes: make(map[*os.Process]struct{})}
}

// start starts cmd, joins it to its process group and tracks it under the lock the exit cleanup takes, so the cleanup
// either kills the command or runs before it starts. After the cleanup it starts nothing and returns errPigExiting.
func (s *detachedChildSet) start(cmd *exec.Cmd) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.killed {
		// nodespawn.Start closes the child's ends of the pipes whether or not it starts; do the same.
		for _, stdio := range []any{cmd.Stdin, cmd.Stdout, cmd.Stderr} {
			if file, ok := stdio.(*os.File); ok && file != nil {
				_ = file.Close()
			}
		}
		return errPigExiting
	}
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

// killAll kills the process group of every running shell command, forgets them and refuses later starts.
func (s *detachedChildSet) killAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.killed = true
	for p := range s.processes {
		_ = killProcessGroup(p)
	}
	clear(s.processes)
}

// KillTrackedDetachedChildren kills the process group of every running shell command and forgets them. pig is exiting:
// a shell command started afterwards fails without running.
func KillTrackedDetachedChildren() {
	detachedChildren.killAll()
}
