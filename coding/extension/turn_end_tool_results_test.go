package extension

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// TurnEndEvent.toolResults is a ToolResultMessage[] (the pi-ai message object): each element writes the "toolResult" role first, keeps an explicit `details: null` the tool wrote, and omits an absent one, so a handler in any extension process sees the object the session records.
//
// mutation-checked: dropping the ToolResultMessage MarshalJSON in agent/message_json.go wrote no role (and, without DetailsNull, no details:null) in the turn_end event.
// upstream: packages/coding-agent/src/core/extensions/types.ts:1028-1032 (TurnEndEvent.toolResults: ToolResultMessage[]), packages/ai/src/types.ts ToolResultMessage
func TestTurnEndEventToolResultsWriteTheToolResultMessageObject(t *testing.T) {
	event := TurnEndEvent{Type: "turn_end", ToolResults: []ToolResultMessage{
		{ToolCallID: "c1", ToolName: "read", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "ok"}}, DetailsNull: true, Timestamp: 7},
		{ToolCallID: "c2", ToolName: "bash", Content: []ai.ToolResultMessageContent{}, IsError: true, Timestamp: 8},
	}}
	got, err := json.Marshal(event.ToolResults)
	if err != nil {
		t.Fatal(err)
	}
	const want = `[{"role":"toolResult","toolCallId":"c1","toolName":"read","content":[{"type":"text","text":"ok"}],"details":null,"isError":false,"timestamp":7},{"role":"toolResult","toolCallId":"c2","toolName":"bash","content":[],"isError":true,"timestamp":8}]`
	if string(got) != want {
		t.Fatalf("toolResults =\n%s\nwant\n%s", got, want)
	}
	message := agent.AgentMessage{ToolResult: &event.ToolResults[0]}
	if viaMessage, _ := json.Marshal(message); string(viaMessage) != `{"role":"toolResult","toolCallId":"c1","toolName":"read","content":[{"type":"text","text":"ok"}],"details":null,"isError":false,"timestamp":7}` {
		t.Fatalf("AgentMessage form = %s", viaMessage)
	}
}
