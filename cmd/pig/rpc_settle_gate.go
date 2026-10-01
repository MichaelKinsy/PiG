// Ports packages/coding-agent/src/modes/rpc/rpc-mode.ts
package main

import (
	"context"
	"sync"

	"github.com/MichaelKinsy/PiG/agent"
)

// pig divergence (D86): Pig reads stdin on a goroutine of its own, so the gate reproduces the ordering that Pi's single event loop gives for free; a settle-tail handler of a runtime that reports no window keeps it closed while the handler runs, where Pi reads stdin.
//
// rpcSettleGate holds stdin input between a published agent_end and the agent_settled that follows it. Pi's path from agent_end to agent_settled (agent-session.ts:1752-1777 _runAgentPrompt, 1044-1058 _emitAgentSettled) runs in microtasks unless it waits on something real, so no stdin line is read inside it (rpc-mode.ts:355-360,808-810). Pig reads stdin on its own goroutine, so a command sent on seeing agent_end would otherwise be answered before agent_settled.
//
// The gate is open while a wait that Pi serves stdin during is pending: a compaction, an auto-retry delay, a new run (agent_start), or a settle-tail extension handler that waits on a timer, I/O or another real wait (subprocess.Host.SetSettleTailHandler). A pending extension dialog is checked by the caller, which also stops waiting when shutdown starts or the mode ends.
type rpcSettleGate struct {
	mu         sync.Mutex
	tail       bool
	compacting bool
	retrying   bool
	// handlerWaits holds each extension host's count of settle-tail handlers that wait on something real; a replacement host reports into a slot of its own.
	handlerWaits []*int
	waiters      []chan struct{}
}

// before observes an event ahead of its publication: a held tail must be visible before a reader can see agent_end.
func (g *rpcSettleGate) before(event agent.AgentEvent) {
	g.mu.Lock()
	defer g.mu.Unlock()
	switch event.(type) {
	case agent.AgentEndEvent:
		g.tail = true
	case agent.CompactionStartEvent:
		g.compacting = true
	case agent.AutoRetryStartEvent:
		g.retrying = true
	}
	g.releaseLocked()
}

// after observes an event once it has been written, so a command admitted by it answers after it.
func (g *rpcSettleGate) after(event agent.AgentEvent) {
	g.mu.Lock()
	defer g.mu.Unlock()
	switch event.(type) {
	case agent.AgentSettledEvent, agent.AgentStartEvent:
		g.tail = false
	case agent.CompactionEndEvent:
		g.compacting = false
	case agent.AutoRetryEndEvent:
		g.retrying = false
	}
	g.releaseLocked()
}

// reset forgets the tail of a Session that a replacement ended without settling it, with the handler counts of the hosts that served it.
func (g *rpcSettleGate) reset() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.tail, g.compacting, g.retrying = false, false, false
	g.handlerWaits = nil
	g.releaseLocked()
}

// tailHandlerReporter returns the callback through which one extension host reports its count of waiting settle-tail handlers.
func (g *rpcSettleGate) tailHandlerReporter() func(waiting int) {
	slot := new(int)
	g.mu.Lock()
	g.handlerWaits = append(g.handlerWaits, slot)
	g.mu.Unlock()
	return func(waiting int) {
		g.mu.Lock()
		defer g.mu.Unlock()
		*slot = waiting
		g.releaseLocked()
	}
}

func (g *rpcSettleGate) handlerWaitsLocked() bool {
	for _, waiting := range g.handlerWaits {
		if *waiting > 0 {
			return true
		}
	}
	return false
}

func (g *rpcSettleGate) openLocked() bool {
	return !g.tail || g.compacting || g.retrying || g.handlerWaitsLocked()
}

func (g *rpcSettleGate) releaseLocked() {
	if !g.openLocked() {
		return
	}
	for _, w := range g.waiters {
		close(w)
	}
	g.waiters = nil
}

// opened is closed once the gate is open. The channel of an open gate is closed already.
func (g *rpcSettleGate) opened() <-chan struct{} {
	g.mu.Lock()
	defer g.mu.Unlock()
	done := make(chan struct{})
	if g.openLocked() {
		close(done)
	} else {
		g.waiters = append(g.waiters, done)
	}
	return done
}

// wait blocks until the gate is open, a dialog waits for a response that only stdin can give, stop closes, or ctx ends.
func (g *rpcSettleGate) wait(ctx context.Context, dialog func() <-chan struct{}, stop <-chan struct{}) {
	opened := g.opened()
	select {
	case <-opened:
		return
	default:
	}
	select {
	case <-opened:
	case <-dialog():
	case <-stop:
	case <-ctx.Done():
	}
}
