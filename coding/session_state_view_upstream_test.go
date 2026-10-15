package coding

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// upstream: agent-session.ts:1426 `get state()` returns this.agent.state: the session's state is the agent's live state, so a write through it is what Session.ThinkingLevel and Session.Model read back (agent-session.ts:1442-1449).
func TestSessionStateIsTheAgentsLiveState(t *testing.T) {
	var starts []string
	session := bindingsSession(t, t.TempDir(), &starts)
	state := session.State()

	state.SetThinkingLevel(ai.ThinkingHigh)
	if got := session.ThinkingLevel(); got != ai.ThinkingHigh {
		t.Fatalf("session thinking level after State().SetThinkingLevel = %q", got)
	}
	if got := session.Agent().State().ThinkingLevel(); got != ai.ThinkingHigh {
		t.Fatalf("the agent's own state disagrees: %q", got)
	}
	model := session.Model()
	if state.Model() != model {
		t.Fatalf("State().Model() = %v, session model %v", state.Model(), model)
	}
}
