package codingagent

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// An extension's model request hands pi-ai its messages as written, so a toolResult with details: null reaches the
// provider (and an extension provider's stream) as null, and one without details has no key.
func TestSubprocessRequestMessagesKeepToolResultDetailsAsPi(t *testing.T) {
	for _, wire := range []string{
		`{"role":"toolResult","toolCallId":"c1","toolName":"probe","content":[],"details":null,"isError":false,"timestamp":1}`,
		`{"role":"toolResult","toolCallId":"c1","toolName":"probe","content":[],"isError":false,"timestamp":1}`,
		`{"role":"toolResult","toolCallId":"c1","toolName":"probe","content":[],"details":{"n":1},"isError":false,"timestamp":1}`,
	} {
		var item any
		if err := json.Unmarshal([]byte(wire), &item); err != nil {
			t.Fatal(err)
		}
		messages, err := subprocessRequestMessages([]any{item})
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(messages[0])
		if err != nil {
			t.Fatal(err)
		}
		if string(encoded) != wire {
			t.Fatalf("request message\n got %s\nwant %s", encoded, wire)
		}
	}
}

// A tool_result handler chain that leaves details: null returns it as null, and agent-loop.ts applies
// `afterResult.details ?? result.details`, so the override keeps the tool's own details.
func TestToolResultEventOverrideTreatsNullDetailsAsNullish(t *testing.T) {
	override := ToolResultEventOverride(&extension.ToolResultEventResult{Details: json.RawMessage("null")})
	if override.Details != nil {
		t.Fatalf("override details %#v, want none", override.Details)
	}
	override = ToolResultEventOverride(&extension.ToolResultEventResult{Details: json.RawMessage(`{"a":1}`)})
	if string(override.Details.(json.RawMessage)) != `{"a":1}` {
		t.Fatalf("override details %#v, want the handler's", override.Details)
	}
}
