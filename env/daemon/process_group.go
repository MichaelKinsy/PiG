package daemon

// Ports packages/env/daemon/src/exec.rs (Ending, start_waiter)

import "sync"

// programGroup is a `process` program's group: what the program started there is killed once it exits, and the daemon's
// kills reach it only while the program is not reaped.
type programGroup struct {
	pid int
	// mu orders the kills against the reap: once the program is forgotten (before it is reaped) a kill signals nothing, so a
	// kill that raced the reap never reaches a reused pid.
	mu   sync.Mutex
	live bool
	// ending kills what is left in the group once the program exited.
	ending func()
}

// end kills what is left in the group, the program too if it still runs.
func (g *programGroup) end() { g.ending() }

// kill kills the group while its program is not reaped.
func (g *programGroup) kill() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.live {
		killTree(g.pid)
	}
}

// forget ends the kills the group answers; the program is reaped after it.
func (g *programGroup) forget() {
	g.mu.Lock()
	g.live = false
	g.mu.Unlock()
}

// waitAndEnd waits until the program exited, kills what is left in its group, forgets the group and reaps the program. After
// a failed wait, ending the group kills the program, so the reap still returns.
func (p *program) waitAndEnd() (int, error) {
	exited := p.group.waitForExit()
	p.group.end()
	p.group.forget()
	code, err := reapProgram(p.cmd)
	if exited != nil {
		return 0, exited
	}
	return code, err
}
