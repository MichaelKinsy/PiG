package agent

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi's toolResult message is `details: finalized.result.details` (agent-loop.ts:922-934), so a tool that returns
// details: null persists and emits `"details":null`, and one that returns none has no details key: JSON.stringify
// omits undefined but writes null.
var toolResultDetailsWire = []struct{ name, wire string }{
	{"explicit null", `{"role":"toolResult","toolCallId":"c1","toolName":"probe","content":[],"details":null,"isError":false,"timestamp":1}`},
	{"absent", `{"role":"toolResult","toolCallId":"c1","toolName":"probe","content":[],"isError":false,"timestamp":1}`},
	{"value", `{"role":"toolResult","toolCallId":"c1","toolName":"probe","content":[],"details":{"b":1,"a":null},"isError":false,"timestamp":1}`},
}

func TestAgentMessageToolResultDetailsRoundTripAsPi(t *testing.T) {
	for _, tc := range toolResultDetailsWire {
		t.Run(tc.name, func(t *testing.T) {
			var message AgentMessage
			if err := json.Unmarshal([]byte(tc.wire), &message); err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(message)
			if err != nil {
				t.Fatal(err)
			}
			if string(encoded) != tc.wire {
				t.Fatalf("round trip\n got %s\nwant %s", encoded, tc.wire)
			}
			// The provider-facing message keeps the same three states.
			call := AgentMessage{Assistant: &AssistantMessage{Role: RoleAssistant, StopReason: ai.StopReasonToolUse, Content: []ai.AssistantContentBlock{ai.ToolCall{ID: "c1", Name: "probe", Arguments: ai.JsonObject{}}}}}
			llm := ConvertToLLM(NormalizeMessages([]AgentMessage{call, message}, nil))
			if len(llm) != 2 {
				t.Fatalf("provider messages %d, want 2", len(llm))
			}
			encoded, err = json.Marshal(llm[1])
			if err != nil {
				t.Fatal(err)
			}
			if string(encoded) != tc.wire {
				t.Fatalf("provider message\n got %s\nwant %s", encoded, tc.wire)
			}
		})
	}
}

// A tool result that holds details: null (a subprocess tool wrote the member) becomes a message with details: null;
// one that never wrote details becomes a message without it.
func TestCreateToolResultMessageKeepsExplicitNullDetails(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result AgentToolResult
		want   string
	}{
		{"explicit null", AgentToolResult{MemberOrder: []string{"content", "details"}}, toolResultDetailsWire[0].wire},
		{"absent", AgentToolResult{MemberOrder: []string{"content"}}, toolResultDetailsWire[1].wire},
		{"Go tool without details", AgentToolResult{}, toolResultDetailsWire[1].wire},
	} {
		t.Run(tc.name, func(t *testing.T) {
			message := createToolResultMessage(finalizedToolCall{call: pendingToolCall{id: "c1", name: "probe"}, result: tc.result}, 1)
			encoded, err := json.Marshal(AgentMessage{ToolResult: &message})
			if err != nil {
				t.Fatal(err)
			}
			if string(encoded) != tc.want {
				t.Fatalf("message\n got %s\nwant %s", encoded, tc.want)
			}
		})
	}
}
