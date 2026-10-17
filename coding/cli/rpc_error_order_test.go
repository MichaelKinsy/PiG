package cli

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// lazy.ts createSetupErrorMessage (and faux.ts) build an error message with errorMessage before timestamp, and
// JSON.stringify keeps that order in RPC and JSON-mode events. A provider's own failure keeps timestamp first.
func TestRPCAssistantMessageKeepsTheErrorFirstOrder(t *testing.T) {
	for _, tc := range []struct {
		name       string
		errorFirst bool
	}{{"setup error", true}, {"provider error", false}} {
		t.Run(tc.name, func(t *testing.T) {
			message := agent.AgentMessage{Assistant: &agent.AssistantMessage{
				Role: agent.RoleAssistant, Content: []ai.AssistantContentBlock{}, StopReason: ai.StopReasonError,
				ErrorMessage: "OAuth refresh failed for demo", ErrorBeforeTimestamp: tc.errorFirst, Timestamp: 7,
			}}
			object, err := rpcAgentMessage(message)
			if err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(object)
			if err != nil {
				t.Fatal(err)
			}
			if got := bytes.Count(data, []byte(`"errorMessage"`)); got != 1 {
				t.Fatalf("errorMessage appears %d times: %s", got, data)
			}
			errorAt, timestampAt := bytes.Index(data, []byte(`"errorMessage"`)), bytes.Index(data, []byte(`"timestamp"`))
			if (errorAt < timestampAt) != tc.errorFirst {
				t.Fatalf("errorFirst=%v: %s", tc.errorFirst, data)
			}
		})
	}
}
