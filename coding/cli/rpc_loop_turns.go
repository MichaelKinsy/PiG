// Ports packages/coding-agent/src/modes/rpc/rpc-mode.ts
package cli

import (
	"sync"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/internal/jsloop"
)

// pig divergence (D5): Pi runs the agent's run and the extensions' timers on one event loop, so a setTimeout or setImmediate callback an extension queued cannot run while the run chains microtasks. It runs when the run awaits real work, or once the loop is idle after agent_settled. An extension runtime is another process, whose own loop runs the callback at once and marks the host calls it makes (CallPayload.Timer); rpcLoopChain tells the host's jsloop.Turns where the run's chain is, so it holds those calls until Pi's loop would run them.
//
// The chain runs from agent_start, published, to the end of agent_settled, written. A compaction and an auto-retry delay are real waits inside it; the run's I/O and tools report their own (agent.Agent.SetLoopTurns).
type rpcLoopChain struct {
	turns *jsloop.Turns
	mu    sync.Mutex
	end   func()
	// compaction and retry end the wait a compaction or an auto-retry delay opened.
	compaction, retry func()
}

// before observes an event ahead of its publication: the chain is running before any extension sees the run's events.
func (c *rpcLoopChain) before(event agent.AgentEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch event.(type) {
	case agent.AgentStartEvent:
		if c.end == nil {
			c.end = c.turns.Chain()
		}
	case agent.CompactionStartEvent:
		if c.compaction == nil {
			c.compaction = c.turns.Wait()
		}
	case agent.AutoRetryStartEvent:
		if c.retry == nil {
			c.retry = c.turns.Wait()
		}
	}
}

// after observes an event once it has been written: the callbacks held for the loop run behind agent_settled, as Pi's loop runs them once the tail's microtasks are over.
func (c *rpcLoopChain) after(event agent.AgentEvent) {
	c.mu.Lock()
	var ends []func()
	switch event.(type) {
	case agent.AgentSettledEvent:
		ends = append(ends, c.end)
		c.end = nil
	case agent.ToolExecutionEndEvent:
		// Pi runs the process.nextTick callbacks a tool queued between its tool_execution_end and the tool result message.
		defer c.turns.Ticked()
	case agent.CompactionEndEvent:
		ends = append(ends, c.compaction)
		c.compaction = nil
	case agent.AutoRetryEndEvent:
		ends = append(ends, c.retry)
		c.retry = nil
	}
	c.mu.Unlock()
	for _, end := range ends {
		if end != nil {
			end()
		}
	}
}

// reset ends every stretch the chain holds open, for a Session that a replacement ended without settling it.
func (c *rpcLoopChain) reset() {
	c.mu.Lock()
	ends := []func(){c.end, c.compaction, c.retry}
	c.end, c.compaction, c.retry = nil, nil, nil
	c.mu.Unlock()
	for _, end := range ends {
		if end != nil {
			end()
		}
	}
}
