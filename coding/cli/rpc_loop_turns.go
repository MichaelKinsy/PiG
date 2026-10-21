// Ports packages/coding-agent/src/modes/rpc/rpc-mode.ts
package cli

import (
	"context"
	"sync"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/internal/jsloop"
)

// pig additive (D189): Pi runs the agent's run and the extensions' timers on one event loop, so a setTimeout or setImmediate callback an extension queued cannot run while the run chains microtasks. It runs when the run awaits real work, or once the loop is idle after agent_settled. An extension runtime is another process, whose own loop runs the callback at once and marks the host calls it makes (CallPayload.Timer); rpcLoopChain tells the host's jsloop.Turns where the run's chain is, so it holds those calls until Pi's loop would run them.
//
// The chain runs from agent_start, published, to the end of agent_settled, written. A compaction and an auto-retry delay are real waits inside it; the run's I/O and tools report their own (agent.Agent.SetLoopTurns).
type rpcLoopChain struct {
	turns *jsloop.Turns
	mu    sync.Mutex
	end   func()
	// compaction and retry end the wait a compaction or an auto-retry delay opened.
	compaction, retry func()
	// command ends the Turns command of the stdin iteration that runs.
	command func()
}

// before observes an event ahead of its publication: the chain is running before any extension sees the run's events.
func (c *rpcLoopChain) before(event agent.AgentEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch event := event.(type) {
	case agent.AgentStartEvent:
		if c.end == nil {
			c.end = c.turns.Chain()
		}
	case agent.ToolExecutionStartEvent:
		c.turns.ToolStarted(event.ToolCallID)
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
	switch event := event.(type) {
	case agent.AgentSettledEvent:
		ends = append(ends, c.end)
		c.end = nil
	case agent.ToolExecutionEndEvent:
		// Pi runs the process.nextTick callbacks a tool queued between its tool_execution_end and the tool result message, and the timer callbacks it queued once its loop turns while a sibling tool awaits; the callbacks of the call that ended run here, whenever their host calls arrived.
		defer c.turns.Ticked(event.ToolCallID)
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

// arm opens the Turns command of the stdin iteration the loop is about to begin, and returns once every extension runtime holds its timer callbacks, before the command's first event reaches any of them. The loop is not held yet, so a runtime that waits for it is not blocked. observeCommands finds the command open and ends it with the iteration.
func (c *rpcLoopChain) arm(ctx context.Context) {
	end := c.turns.Arm(ctx)
	c.mu.Lock()
	if c.command == nil {
		c.command, end = end, nil
	}
	c.mu.Unlock()
	if end != nil {
		end()
	}
}

// observeCommands marks the iterations of the responses loop that handle a stdin command as Turns commands: from Begin to the end of the drain that writes the response, the extension runtimes hold their timer callbacks (jsloop.Turns.Command).
func (c *rpcLoopChain) observeCommands(responses *jsloop.Loop) {
	responses.SetBatchObserver(func(open bool) {
		c.mu.Lock()
		defer c.mu.Unlock()
		switch {
		case open && c.command == nil:
			c.command = c.turns.Command()
		case !open && c.command != nil:
			c.command()
			c.command = nil
		}
	})
}
