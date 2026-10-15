package codingagent

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/sessionentry"
)

// The entry built from a typed message is the entry decoding its record gives: the expectation is the decoded record, never PiG's direct form.
func TestDirectMessageEntryMatchesDecode(t *testing.T) {
	i64 := func(n int64) *int64 { return &n }
	one := 1
	usage := &ai.Usage{Input: 3, Output: 4, TotalTokens: 7, Cost: ai.UsageCost{Input: 0.5, Total: 0.5}}
	cases := []struct {
		name   string
		msg    agent.AgentMessage
		direct bool
	}{
		{"user text", agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: ai.UserText("hi <b> &  "), Timestamp: 5}}, true},
		{"user blocks", agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: ai.UserContentBlocks{ai.TextContent{Text: "a"}, ai.ImageContent{Data: "xx", MimeType: "image/png"}}, Timestamp: 5}}, true},
		{"assistant thinking and call", agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", Timestamp: 9, Usage: usage, API: "anthropic-messages", Provider: "p", ModelID: "m", StopReason: ai.StopReason("toolUse"),
			Content: []ai.AssistantContentBlock{ai.ThinkingContent{Thinking: "t", ThinkingSignature: "sig"}, ai.TextContent{Text: "x"},
				ai.ToolCall{ID: "c1", Name: "read", Arguments: ai.JsonObject{"path": "a b", "n": 1.5, "nested": map[string]any{"z": []any{true, nil, "s"}, "a": 2.0}}}}}}, true},
		{"assistant split usage", agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", Timestamp: 9, Usage: &ai.Usage{CacheWrite1h: &one, Reasoning: &one}, Content: []ai.AssistantContentBlock{}}}, true},
		{"assistant duration", agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", Timestamp: 9, DurationMs: i64(12), Content: []ai.AssistantContentBlock{ai.TextContent{Text: "x"}}}}, true},
		{"assistant error", agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", Timestamp: 9, StopReason: "error", ErrorMessage: "boom", Content: []ai.AssistantContentBlock{}}}, true},
		{"tool result details object", agent.AgentMessage{ToolResult: &agent.ToolResultMessage{Role: "toolResult", ToolCallID: "c", ToolName: "t", Timestamp: 1, Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "ok"}}, Details: json.RawMessage(`{"b":1,"a":[2]}`)}}, true},
		{"tool result details null", agent.AgentMessage{ToolResult: &agent.ToolResultMessage{Role: "toolResult", ToolCallID: "c", ToolName: "t", Timestamp: 1, Content: []ai.ToolResultMessageContent{}, DetailsNull: true}}, true},
		{"tool result no details", agent.AgentMessage{ToolResult: &agent.ToolResultMessage{Role: "toolResult", ToolCallID: "c", ToolName: "t", Timestamp: 1, Content: []ai.ToolResultMessageContent{}, IsError: true}}, true},
		{"negative zero argument", agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{ai.ToolCall{ID: "c", Name: "n", Arguments: ai.JsonObject{"v": math.Copysign(0, -1)}}}}}, false},
		{"non-finite argument", agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{ai.ToolCall{ID: "c", Name: "n", Arguments: ai.JsonObject{"v": math.Inf(1)}}}}}, false},
		{"non-UTF-8 text", agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: ai.UserText("a\xffb")}}, false},
		{"non-plain argument type", agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{ai.ToolCall{ID: "c", Name: "n", Arguments: ai.JsonObject{"v": 3}}}}}, false},
		{"details with spaced raw text", agent.AgentMessage{ToolResult: &agent.ToolResultMessage{Role: "toolResult", ToolCallID: "c", ToolName: "t", Content: []ai.ToolResultMessageContent{}, Details: json.RawMessage(`{"a": 1}`)}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base := SessionEntryBase{Type: "message", ID: "abc12345", Timestamp: "2026-01-01T00:00:00.000Z"}
			entry := MessageEntry{SessionEntryBase: base, Message: tc.msg}
			raw, err := marshalSessionLine(entry)
			if err != nil {
				if tc.direct {
					t.Fatal(err)
				}
				if _, ok := tc.msg.DecodedForm(); ok {
					t.Fatal("a message that does not encode has a direct form")
				}
				return
			}
			form, ok := tc.msg.DecodedForm()
			if ok != tc.direct {
				t.Fatalf("direct form = %v, want %v", ok, tc.direct)
			}
			if !ok {
				return
			}
			direct, ok := sessionentry.NewDecodedMessageEntry(base, form, raw)
			if !ok {
				t.Fatal("entry refused")
			}
			want := sessionentry.DecodeSessionEntry(raw)
			if !reflect.DeepEqual(direct, want) {
				t.Fatalf("direct entry differs from the decoded record\n got %#v\nwant %#v", direct, want)
			}
			if string(direct.Raw()) != string(raw) || string(want.Raw()) != string(raw) {
				t.Fatalf("record changed")
			}
		})
	}
}
