package coding

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// sameContextMessages decides, by value, whether a `context` handler left the conversation unchanged: a subprocess handler returns decoded JSON, never the session's own values.
func TestSameContextMessagesComparesDecodedMessagesByValue(t *testing.T) {
	visible := []extension.AgentMessage{
		{User: &agent.UserMessage{Role: "user", Content: ai.UserText("hello")}},
		{Assistant: &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{
			ai.TextContent{Text: "reading"},
			ai.ToolCall{ID: "c1", Name: "read", Arguments: ai.JsonObject{"path": "b.go", "limit": float64(5), "a": []any{"x"}}},
		}}},
	}
	decode := func(edit func(raw []map[string]any)) []extension.AgentMessage {
		encoded, err := json.Marshal(visible)
		if err != nil {
			t.Fatal(err)
		}
		var raw []map[string]any
		if err := json.Unmarshal(encoded, &raw); err != nil {
			t.Fatal(err)
		}
		edit(raw)
		reencoded, err := json.Marshal(raw)
		if err != nil {
			t.Fatal(err)
		}
		var returned []extension.AgentMessage
		if err := json.Unmarshal(reencoded, &returned); err != nil {
			t.Fatal(err)
		}
		return returned
	}
	if !sameContextMessages(decode(func([]map[string]any) {}), visible) {
		t.Error("a decoded copy with the same values compared as changed")
	}
	edited := decode(func(raw []map[string]any) { raw[0]["content"] = "hello there" })
	if sameContextMessages(edited, visible) {
		t.Error("an edited user message compared as unchanged")
	}
	if sameContextMessages(decode(func([]map[string]any) {})[:1], visible) {
		t.Error("a shorter list compared as unchanged")
	}
	reordered := decode(func([]map[string]any) {})
	reordered[0], reordered[1] = reordered[1], reordered[0]
	if sameContextMessages(reordered, visible) {
		t.Error("a reordered list compared as unchanged")
	}
}

// A handler may return an equal details object with its keys in another order (a JavaScript or Python handler that rebuilds the object). The decoded copy keeps the handler's key order, so the bytes differ although the values are equal, and the decoded comparison decides.
func TestSameContextMessagesComparesDetailsWhoseKeyOrderDiffersByValue(t *testing.T) {
	type details struct {
		Zeta  int    `json:"zeta"`
		Alpha string `json:"alpha"`
	}
	visible := []extension.AgentMessage{{ToolResult: &agent.ToolResultMessage{Role: "toolResult", ToolCallID: "c1", ToolName: "read", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "ok"}}, Details: details{Zeta: 1, Alpha: "a"}}}}
	var returned []extension.AgentMessage
	if err := json.Unmarshal([]byte(`[{"role":"toolResult","toolCallId":"c1","toolName":"read","content":[{"type":"text","text":"ok"}],"details":{"alpha":"a","zeta":1},"isError":false,"timestamp":0}]`), &returned); err != nil {
		t.Fatal(err)
	}
	left, _ := json.Marshal(returned[0])
	right, _ := json.Marshal(visible[0])
	if bytes.Equal(left, right) {
		t.Fatalf("the decoded copy marshals to the same bytes (%s), so this case does not reach the decoded comparison", left)
	}
	if !sameContextMessages(returned, visible) {
		t.Error("details with the same values in another key order compared as changed")
	}
	changed := []extension.AgentMessage{{ToolResult: &agent.ToolResultMessage{Role: "toolResult", ToolCallID: "c1", ToolName: "read", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "ok"}}, Details: map[string]any{"alpha": "a", "zeta": float64(2)}}}}
	if sameContextMessages(changed, visible) {
		t.Error("details with another value compared as unchanged")
	}
}
