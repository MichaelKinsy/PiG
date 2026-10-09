package cli

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
)

// Pi 1.1.0 agent-loop.ts createToolResultMessage records how long execute() took: durationMs sits between isError and
// timestamp on the tool-result message, in message_start/message_end, turn_end toolResults and the session. A result of a
// tool that did not run has none.
func TestRPCToolResultMessageCarriesDurationMsBetweenIsErrorAndTimestamp(t *testing.T) {
	for _, tc := range []struct {
		durationMs *int64
		want       string
	}{
		{new(int64(10)), `{"role":"toolResult","toolCallId":"c1","toolName":"bash","content":[],"isError":true,"durationMs":10,"timestamp":5}`},
		{nil, `{"role":"toolResult","toolCallId":"c1","toolName":"bash","content":[],"isError":true,"timestamp":5}`},
	} {
		result := agent.ToolResultMessage{Role: agent.RoleToolResult, ToolCallID: "c1", ToolName: "bash", IsError: true, DurationMs: tc.durationMs, Timestamp: 5}
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
			if string(got) != tc.want {
				t.Errorf("%s: wire = %s\nwant %s", name, got, tc.want)
			}
		}
	}
}
