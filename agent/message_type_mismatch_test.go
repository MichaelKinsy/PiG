package agent

import (
	"bytes"
	stdjson "encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
)

// JSON.parse keeps a member of any type, so Pi keeps a session message whose member has another type than the one Pi writes and writes it unchanged (session-manager.ts parseSessionEntryLine, buildSessionContext). Each line of the fixture holds such members at the top level or in a nested block or record.
func TestDecodedMessageKeepsMembersOfAnyType(t *testing.T) {
	data, err := os.ReadFile("testdata/poisoned-messages.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	var messages []AgentMessage
	typed, typedLines := 0, 0
	for line := range bytes.SplitSeq(bytes.TrimSpace(data), []byte("\n")) {
		for _, role := range []string{`"role":"user"`, `"role":"assistant"`, `"role":"toolResult"`} {
			if bytes.Contains(line, []byte(role)) {
				typedLines++
			}
		}
		var message AgentMessage
		if err := message.UnmarshalJSON(line); err != nil {
			t.Errorf("Pi keeps %s; PiG: %v", line, err)
			continue
		}
		messages = append(messages, message)
		// The context projects { ...message, content: [] } ("" for system) when a typed role's content is null or missing (session-manager.ts:439-451), whatever its other members hold.
		var value any
		if err := stdjson.Unmarshal(line, &value); err != nil {
			t.Fatal(err)
		}
		object, isObject := value.(map[string]any)
		// A message that is not an object has no role, so Pi's convertToLlm sends nothing for it (messages.ts:189-192).
		if !isObject && (message.Custom == nil || message.Role() != "" || len(ConvertToLLM([]AgentMessage{message})) != 0) {
			t.Errorf("%s: decoded as %+v, want a custom message without a role that the provider never sees", line, message)
		}
		role, _ := object["role"].(string)
		wantNull := object["content"] == nil && (role == "system" || role == RoleUser || role == RoleAssistant || role == RoleToolResult)
		if got := message.NullContent(); got != wantNull {
			t.Errorf("%s: NullContent() = %v, want %v", line, got, wantNull)
		}
		for marker, check := range readsAs {
			if bytes.Contains(line, []byte(marker)) {
				check(t, message)
			}
		}
		if message.User != nil || message.Assistant != nil || message.ToolResult != nil {
			typed++
		}
		want, err := jsonstringify.Canonicalize(line)
		if err != nil {
			t.Fatal(err)
		}
		got, err := message.MarshalJSON()
		if err != nil {
			t.Errorf("%s: write: %v", line, err)
			continue
		}
		if !bytes.Equal(got, want) {
			t.Errorf("written\n got %s\nwant %s", got, want)
		}
		clone := message.Clone()
		if got, err := clone.MarshalJSON(); err != nil || !bytes.Equal(got, want) {
			t.Errorf("clone written\n got %s (%v)\nwant %s", got, err, want)
		}
	}
	// The provider boundary reads what the fields hold: each user, assistant and tool result message reaches it.
	converted := 0
	for _, message := range ConvertToLLM(messages) {
		switch message.(type) {
		case ai.UserMessage, ai.AssistantMessage, ai.ToolResultMessage:
			converted++
		}
	}
	if typed != typedLines || converted < typed {
		t.Errorf("%d user, assistant and tool result messages decoded (want %d), %d reach the provider", typed, typedLines, converted)
	}
}

// readsAs checks what the fields of a fixture message hold, keyed by a text unique to its line: a member of another type reads as absent, an object or array member keeps the members and elements its field holds, and a content block the role cannot hold is left out.
var readsAs = map[string]func(*testing.T, AgentMessage){
	`"timestamp string"`: func(t *testing.T, m AgentMessage) {
		if m.User.Content != ai.UserText("timestamp string") || m.User.Timestamp != 0 {
			t.Errorf("user = %+v", m.User)
		}
	},
	`"content":5,"timestamp":1}`: func(t *testing.T, m AgentMessage) {
		if blocks, ok := m.User.Content.(ai.UserContentBlocks); !ok || len(blocks) != 0 || m.User.Timestamp != 1 {
			t.Errorf("user = %+v", m.User)
		}
	},
	`"textSignature":5`: func(t *testing.T, m AgentMessage) {
		if want := (ai.UserContentBlocks{ai.TextContent{Text: "ok"}}); !reflect.DeepEqual(m.User.Content, want) {
			t.Errorf("content = %#v, want %#v", m.User.Content, want)
		}
	},
	`"bare string block"`: func(t *testing.T, m AgentMessage) {
		if blocks, ok := m.User.Content.(ai.UserContentBlocks); !ok || len(blocks) != 0 || m.User.Timestamp != 7 {
			t.Errorf("user = %+v", m.User)
		}
	},
	`"usage members"`: func(t *testing.T, m AgentMessage) {
		if u := m.Assistant.Usage; u == nil || !reflect.DeepEqual(*u, ai.Usage{}) || m.Assistant.ModelID != "m" || m.Assistant.StopReason != ai.StopReasonStop || m.Assistant.Timestamp != 12 {
			t.Errorf("assistant = %+v", m.Assistant)
		}
	},
	`"cost members"`: func(t *testing.T, m AgentMessage) {
		if usage := m.Assistant.Usage; usage == nil || usage.Input != 1 || usage.TotalTokens != 2 || !reflect.DeepEqual(usage.Cost, ai.UsageCost{}) {
			t.Errorf("usage = %+v", usage)
		}
	},
	`"error members"`: func(t *testing.T, m AgentMessage) {
		if a := m.Assistant; a.StopReason != ai.StopReasonError || a.ErrorMessage != "" || a.DurationMs != nil || a.EndTurn != nil || a.Timestamp != 14 {
			t.Errorf("assistant = %+v", a)
		}
	},
	`"record members"`: func(t *testing.T, m AgentMessage) {
		if a := m.Assistant; len(a.Diagnostics) != 1 || a.Diagnostics[0].Type != "" || a.Deferred == nil || a.Deferred.Provider != "p" || a.Deferred.ExpiresAt != nil {
			t.Errorf("diagnostics = %+v, deferred = %+v", a.Diagnostics, a.Deferred)
		}
	},
	`"arguments":[1,2]`: func(t *testing.T, m AgentMessage) {
		if len(m.Assistant.Content) != 1 {
			t.Fatalf("content = %#v", m.Assistant.Content)
		}
		if call, ok := m.Assistant.Content[0].(ai.ToolCall); !ok || call.ID != "c1" || call.Name != "read" || call.Arguments != nil {
			t.Errorf("content = %#v", m.Assistant.Content)
		}
	},
	`"isError":"no"`: func(t *testing.T, m AgentMessage) {
		if r := m.ToolResult; r.ToolCallID != "" || r.ToolName != "" || r.IsError || !reflect.DeepEqual(r.Content, []ai.ToolResultMessageContent{ai.TextContent{Text: "ids"}}) {
			t.Errorf("tool result = %+v", r)
		}
	},
	`"nested members"`: func(t *testing.T, m AgentMessage) {
		if calls := m.ToolResult.NestedCalls; calls == nil || len(calls.Calls) != 1 || calls.Calls[0].ID != "" || calls.Complete {
			t.Errorf("nested calls = %+v", calls)
		}
	},
	`"content":"sections"`: func(t *testing.T, m AgentMessage) {
		if m.System == nil || m.System.Content != ai.SystemText("sections") || m.System.Sections != nil || m.System.Timestamp != 0 {
			t.Errorf("system = %+v", m.System)
		}
	},
	`"role number"`: func(t *testing.T, m AgentMessage) {
		if m.Custom == nil || m.Role() != "" || m.Custom["content"] != "role number" {
			t.Errorf("message = %#v", m)
		}
	},
}
