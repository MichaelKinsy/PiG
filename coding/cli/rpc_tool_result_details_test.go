package cli

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
)

// Pi's RPC mode writes the AgentMessage object with JSON.stringify (rpc-mode.ts output), so a persisted toolResult
// read back from the session keeps details: null as null and an absent details as no key (agent-loop.ts:922-934).
func TestRPCToolResultMessageKeepsDetailsAsPi(t *testing.T) {
	for _, wire := range []string{
		`{"role":"toolResult","toolCallId":"c1","toolName":"probe","content":[],"details":null,"isError":false,"timestamp":1}`,
		`{"role":"toolResult","toolCallId":"c1","toolName":"probe","content":[],"isError":false,"timestamp":1}`,
		`{"role":"toolResult","toolCallId":"c1","toolName":"probe","content":[],"details":{"b":1,"a":2},"isError":false,"timestamp":1}`,
	} {
		var message agent.AgentMessage
		if err := json.Unmarshal([]byte(wire), &message); err != nil {
			t.Fatal(err)
		}
		out, err := rpcAgentMessage(message)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(out)
		if err != nil {
			t.Fatal(err)
		}
		if string(encoded) != wire {
			t.Fatalf("rpc message\n got %s\nwant %s", encoded, wire)
		}
	}
}
