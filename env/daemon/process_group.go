package daemon

// Ports packages/env/daemon/src/exec.rs (Groups, Registered, Ending, start_waiter)

import (
	"sync"
	"sync/atomic"
)

// nextRegistrationToken issues a unique token for each program registration in a groupRegistry.
var nextRegistrationToken atomic.Uint64

// groupRegistry tracks commands this daemon started and has not reaped, by pid, each with the token of its
// registration (a pid names another process once reaped, which another command may register).
type groupRegistry struct {
	mu      sync.Mutex
	entries map[int]uint64
}

func newGroupRegistry() *groupRegistry {
	return &groupRegistry{entries: make(map[int]uint64)}
}

// registered is one command's registration in a groupRegistry: only it removes or signals that entry, never a later
// command's with the same pid.
type registered struct {
	pid   int
	token uint64
}

// register registers the command pid just started in r.
func (r *groupRegistry) register(pid int) registered {
	token := nextRegistrationToken.Add(1)
	r.mu.Lock()
	if r.entries == nil {
		r.entries = make(map[int]uint64)
	}
	r.entries[pid] = token
	r.mu.Unlock()
	return registered{pid: pid, token: token}
}

// forget removes this registration, if it still stands (a later command with the same pid keeps its own).
func (reg registered) forget(r *groupRegistry) {
	r.mu.Lock()
	if r.entries[reg.pid] == reg.token {
		delete(r.entries, reg.pid)
	}
	r.mu.Unlock()
}

// kill kills the command's group while this registration stands: the waiter forgets it under the same lock before it
// reaps the command, so the pid names no other process here.
func (reg registered) kill(r *groupRegistry) bool {
	return reg.killWith(r, killTree)
}

// killWith is kill with the signal function signal sends to the pid: reports whether this registration stood (and
// signal was called).
func (reg registered) killWith(r *groupRegistry, signal func(int)) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	stands := r.entries[reg.pid] == reg.token
	if stands {
		signal(reg.pid)
	}
	return stands
}

// programGroup is a `process` or `pty` program's group: what the program started there is killed once it exits, and the
// daemon's kills reach it only while the program is not reaped.
type programGroup struct {
	pid int
	// mu orders the kills against the reap: once the program is forgotten (before it is reaped) a kill signals nothing, so a
	// kill that raced the reap never reaches a reused pid.
	mu   sync.Mutex
	live bool
	// ending kills what is left in the group once the program exited.
	ending func()
	// registry and reg, when set, coordinate with a shared pid->token registry so a reused pid is never forgotten or
	// signalled by an earlier registration.
	registry *groupRegistry
	reg      registered
}

// end kills what is left in the group, the program too if it still runs.
func (g *programGroup) end() { g.ending() }

// kill kills the group while its program is not reaped.
func (g *programGroup) kill() {
	if g.registry != nil {
		g.reg.kill(g.registry)
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.live {
		killTree(g.pid)
	}
}

// forget ends the kills the group answers; the program is reaped after it.
func (g *programGroup) forget() {
	if g.registry != nil {
		g.reg.forget(g.registry)
		return
	}
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
