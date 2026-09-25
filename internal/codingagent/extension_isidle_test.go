package codingagent

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
)

// The extension-facing isIdle() latched false permanently when the turn-end
// reset was dropped.
//
// m.isIdle is reset inside a runOnMain callback. runOnMain abandons the callback
// when its context finishes before the main loop drains the queue, so a single
// dropped reset left every extension reading a working agent for the life of the
// process. A fleet supervisor cannot then tell a finished worker from a busy one.
//
// Upstream derives the value on read from _isAgentRunActive
// (agent-session.ts:883) and hands extensions that getter
// (interactive-mode.ts:1980), so it structurally cannot latch. Reading live
// state here removes a divergence rather than adding one.
func TestExtensionIsIdleSurvivesADroppedTurnEndCallback(t *testing.T) {
	m := &InteractiveMode{agent: agent.NewAgent(agent.AgentOptions{})}

	if !m.extensionIsIdle() {
		t.Fatal("a session that has run nothing reported busy")
	}

	m.turnActive.Store(true)
	m.isIdle = false
	if m.extensionIsIdle() {
		t.Fatal("an in-flight turn reported idle")
	}

	// The run goroutine unwinds: turnActive clears synchronously, and the queued
	// isIdle reset is dropped because runCtx finished before the loop drained it.
	m.turnActive.Store(false)
	// m.isIdle deliberately left false: this is the dropped-callback state.

	if !m.extensionIsIdle() {
		t.Fatal("extension isIdle() stayed false after the turn ended with its " +
			"reset dropped; supervisors wait forever on a finished agent")
	}
}

// runOnMain drops the callback when the context is already done, which is the
// mechanism behind the latch. Pinning it keeps the fix bound to a real behavior
// rather than an assumption about the queue.
func TestRunOnMainDropsWorkWhenTheContextIsAlreadyDone(t *testing.T) {
	m := &InteractiveMode{uiTaskCh: make(chan func())} // unbuffered, nothing draining
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	ran := false
	m.runOnMain(ctx, func() { ran = true })

	if ran {
		t.Fatal("callback ran despite a cancelled context")
	}
	// The drop itself is upstream-faithful; what must not depend on it is state
	// an extension reads.
	select {
	case <-m.uiTaskCh:
		t.Fatal("callback was queued despite a cancelled context")
	default:
	}
}

// agent_settled must observe an idle agent. Upstream clears _isAgentRunActive
// before emitting it (agent-session.ts:597); pig emits after runOnMain has only
// accepted the reset, so a cached read reported busy at the moment the run
// finished. turnActive clears before both.
func TestExtensionIsIdleIsTrueByTheTimeAgentSettledCanBeObserved(t *testing.T) {
	m := &InteractiveMode{agent: agent.NewAgent(agent.AgentOptions{})}
	m.turnActive.Store(true)
	m.isIdle = false

	// Ordering inside runTurn's defer: turnActive first, then the queued UI
	// reset, then emitAgentSettled.
	m.turnActive.Store(false)

	if !m.extensionIsIdle() {
		t.Fatal("agent_settled would report a busy agent, unlike upstream")
	}
}
