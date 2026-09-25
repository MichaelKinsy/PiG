package main

import (
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// Upstream writes the session's own AssistantMessage with JSON.stringify: a
// message without a stop reason has no stopReason key. Pig reported it as
// "stop", which a client reads as a completed response. GUARD-16.
func TestRPCAssistantMessagePassesMissingStopReasonThrough(t *testing.T) {
	for _, tc := range []struct {
		reason ai.StopReason
		want   any
		has    bool
	}{
		{"", nil, false},
		{ai.StopReasonStop, ai.StopReasonStop, true},
		{ai.StopReasonToolUse, ai.StopReasonToolUse, true},
	} {
		wire, err := rpcAgentMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, StopReason: tc.reason}})
		if err != nil {
			t.Fatal(err)
		}
		got, has := wire.(map[string]any)["stopReason"]
		if has != tc.has || (has && got != tc.want) {
			t.Errorf("stop reason %q: stopReason = %v (present %v), want %v (present %v)", tc.reason, got, has, tc.want, tc.has)
		}
	}
}
