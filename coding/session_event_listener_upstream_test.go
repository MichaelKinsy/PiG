package coding

import (
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// packages/coding-agent/src/core/agent-session.ts:240,1369-1378: subscribe(listener: AgentSessionEventListener) returns an unsubscribe that removes only that listener, so a later turn reaches the remaining listener and not the removed one.
func TestSessionSubscribeAgentSessionEventListenerUnsubscribesOnlyItself(t *testing.T) {
	h := newBoundaryHarness(t, harnessOptions{}, boundaryReply("one", ai.StopReasonStop, 0), boundaryReply("two", ai.StopReasonStop, 0))
	var removed, kept int
	var removedListener AgentSessionEventListener = func(agent.AgentEvent) { removed++ }
	var keptListener AgentSessionEventListener = func(agent.AgentEvent) { kept++ }
	unsubscribe := h.session.Subscribe(removedListener)
	h.session.Subscribe(keptListener)
	boundaryPrompt(t, h, "first")
	if removed == 0 || kept != removed {
		t.Fatalf("both listeners must see the first turn: removed=%d kept=%d", removed, kept)
	}
	unsubscribe()
	before := removed
	boundaryPrompt(t, h, "second")
	if removed != before {
		t.Fatalf("an unsubscribed listener saw %d more events", removed-before)
	}
	if kept <= before {
		t.Fatalf("the remaining listener must still see the second turn: kept=%d before=%d", kept, before)
	}
}
