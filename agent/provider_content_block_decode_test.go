package agent

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Session content blocks from older PiG persistence can use provider spellings.
// The persistence reader reduces them into the closed transcript union.
func TestProviderSpelledContentBlocksDecode(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		want ai.ContentBlock
	}{
		{
			name: "tool_use from a persisted assistant message",
			raw: `{"role":"assistant","content":[{"type":"tool_use","id":"call_2a9e2cb34c3543ed856d77b5",` +
				`"name":"bash","input":{"command":"fd -e json ."}}]}`,
			want: ai.ToolCall{
				ID: "call_2a9e2cb34c3543ed856d77b5", Name: "bash",
				Arguments: ai.JsonObject{"command": "fd -e json ."},
			},
		},
		{
			name: "tool_result from a persisted toolResult message",
			raw: `{"role":"toolResult","toolCallId":"call_2a9e2cb34c3543ed856d77b5","toolName":"bash",` +
				`"content":[{"type":"tool_result","tool_use_id":"call_2a9e2cb34c3543ed856d77b5","content":"ok"}]}`,
			want: ai.TextContent{Text: "ok"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var msg AgentMessage
			if err := json.Unmarshal([]byte(tc.raw), &msg); err != nil {
				t.Fatalf("decode failed, message would render as unknown: %v", err)
			}
			var got []ai.ContentBlock
			switch {
			case msg.Assistant != nil:
				for _, block := range msg.Assistant.Content {
					got = append(got, block)
				}
			case msg.ToolResult != nil:
				for _, block := range msg.ToolResult.Content {
					got = append(got, block)
				}
			default:
				t.Fatal("no typed message decoded")
			}
			if len(got) != 1 {
				t.Fatalf("got %d blocks, want 1", len(got))
			}
			if !reflect.DeepEqual(got[0], tc.want) {
				t.Errorf("block = %#v, want %#v", got[0], tc.want)
			}
		})
	}
}

// Upstream's own spelling must keep decoding to the same internal block, so the
// forward-compatible default cannot quietly take over the supported path.
func TestUpstreamToolCallSpellingStillDecodes(t *testing.T) {
	var msg AgentMessage
	raw := `{"role":"assistant","content":[{"type":"toolCall","id":"c1","name":"bash","arguments":{"command":"ls"}}]}`
	if err := json.Unmarshal([]byte(raw), &msg); err != nil {
		t.Fatalf("upstream toolCall must decode: %v", err)
	}
	want := ai.ToolCall{ID: "c1", Name: "bash", Arguments: ai.JsonObject{"command": "ls"}}
	if !reflect.DeepEqual(msg.Assistant.Content[0], want) {
		t.Errorf("block = %#v, want %#v", msg.Assistant.Content[0], want)
	}
}

// A block type nobody has taught the reader about must cost that block, not the
// whole message. ai.UnmarshalContentBlock keeps it as raw text.
func TestUnknownContentBlockRejectsTheMessage(t *testing.T) {
	var msg AgentMessage
	raw := `{"role":"assistant","content":[{"type":"text","text":"before"},{"type":"someFutureBlock","x":1}]}`
	if err := json.Unmarshal([]byte(raw), &msg); err == nil {
		t.Fatal("unknown block must reject the closed content union")
	}
}

// Closed tool-result content marshals and round-trips directly.
func TestToolResultContentRoundTrips(t *testing.T) {
	msg := AgentMessage{ToolResult: &ToolResultMessage{
		Role: RoleToolResult, ToolCallID: "c1", ToolName: "bash",
		Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "output"}},
	}}
	encoded, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal must not reject a tool result block: %v", err)
	}
	var back AgentMessage
	if err := json.Unmarshal(encoded, &back); err != nil {
		t.Fatalf("re-read failed: %v", err)
	}
	if len(back.ToolResult.Content) != 1 {
		t.Fatalf("got %d blocks, want 1", len(back.ToolResult.Content))
	}
	got, ok := back.ToolResult.Content[0].(ai.TextContent)
	if !ok || got.Text != "output" || back.ToolResult.ToolCallID != "c1" {
		t.Errorf("round trip lost data: %#v", back.ToolResult.Content[0])
	}
}
