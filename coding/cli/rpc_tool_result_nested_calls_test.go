package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// Pi 1.1.0 agent-session.ts sets message.nestedCalls after createToolResultMessage built the message (agent-loop.ts:926-941), so
// on the RPC and JSON wire nestedCalls follows timestamp. A result without nested calls has no nestedCalls key.
func TestRPCToolResultMessageCarriesNestedCallsAfterTimestamp(t *testing.T) {
	nested := &ai.NestedToolCalls{Calls: []ai.NestedToolCallRecord{}, Complete: true}
	for _, tc := range []struct {
		nested *ai.NestedToolCalls
		suffix string
	}{
		{nested, `"isError":false,"durationMs":7,"timestamp":5,"nestedCalls":{"calls":[],"complete":true}}`},
		{nil, `"isError":false,"durationMs":7,"timestamp":5}`},
	} {
		result := agent.ToolResultMessage{Role: agent.RoleToolResult, ToolCallID: "c1", ToolName: "codemode", DurationMs: new(int64(7)), Timestamp: 5, NestedCalls: tc.nested}
		for name, encode := range map[string]func() (any, error){
			"message": func() (any, error) { return rpcAgentMessage(agent.AgentMessage{ToolResult: &result}) },
			"turn_end": func() (any, error) {
				messages, err := rpcToolResultMessages([]agent.ToolResultMessage{result})
				return messages[0], err
			},
		} {
			value, err := encode()
			if err != nil {
				t.Fatal(err)
			}
			got, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasSuffix(string(got), tc.suffix) {
				t.Errorf("%s: wire = %s\nwant suffix %s", name, got, tc.suffix)
			}
		}
	}
}
