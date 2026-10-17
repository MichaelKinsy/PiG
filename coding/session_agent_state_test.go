package coding

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// upstream: agent-session.ts:1426 `get state(): AgentState { return this.agent.state; }`: the session's state is its agent's, including later writes.
func TestSessionStateIsTheAgentState(t *testing.T) {
	svcs := newTestServices(t)
	model := fakeModel()
	model.Capabilities.MaxThinking = ai.ThinkingLevelHigh
	sess, err := NewSession(svcs, SessionOptions{Model: model})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()
	if err := sess.SetThinkingLevel(ai.ThinkingHigh); err != nil {
		t.Fatal(err)
	}
	state := sess.State().Snapshot()
	want := sess.Agent().State().Snapshot()
	if state.Model != sess.Agent().Model() || state.ThinkingLevel != ai.ThinkingHigh || state.ThinkingLevel != want.ThinkingLevel {
		t.Fatalf("state = model %v thinking %q, want the agent's model and %q", state.Model, state.ThinkingLevel, ai.ThinkingHigh)
	}
	if state.SystemPrompt != want.SystemPrompt || len(state.Messages) != len(want.Messages) || len(state.Tools) != len(want.Tools) || state.IsStreaming {
		t.Fatalf("state = %+v, want the agent's %+v", state, want)
	}
}
