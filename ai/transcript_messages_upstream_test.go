package ai

import (
	"reflect"
	"testing"
)

// upstream: packages/ai/test/system-message-replay.test.ts:20-58 "replays content, sections, and tools into one leading message" over the TranscriptMessages of utils/transcript.ts:40 (`readonly { role: string }[]`).
// Pi's history is replayed twice: as a TranscriptMessages[Message] and as a TranscriptMessages of app-defined role values with a "bashExecution" and a "branchSummary" entry interleaved, as the interactive transcript carries them (the helpers read only the system entries). Both give Pi's expected current message {content "base\n\nalso do this", sections {a: "<a>2</a>", c: "<c>1</c>"} (b removed by null), toolsAdded [second], timestamp 10} and prompt "base\n\nalso do this\n\n<a>2</a>\n\n<c>1</c>", and the same current tools.
// mutation-checked: the replay ignoring toolsRemoved, taking the last timestamp instead of the first, and not removing a section whose later value is null each fail it.
func TestTranscriptMessagesReplayIntoOneLeadingMessageUpstream(t *testing.T) {
	str := func(s string) *string { return &s }
	first := SystemMessage{Content: SystemText("base"), Sections: OrderedSections{{Name: "a", Value: str("<a>1</a>")}, {Name: "b", Value: str("<b>1</b>")}}, ToolsAdded: []ToolSchema{{Name: "first", Description: "first tool", Parameters: map[string]any{"type": "object"}}}, Timestamp: 10}
	also := SystemMessage{Content: SystemText("also do this"), Timestamp: 12}
	last := SystemMessage{Content: SystemText(""), Sections: OrderedSections{{Name: "a", Value: str("<a>2</a>")}, {Name: "b"}, {Name: "c", Value: str("<c>1</c>")}}, ToolsRemoved: []ToolReference{{Name: "first"}}, ToolsAdded: []ToolSchema{{Name: "second", Description: "second tool", Parameters: map[string]any{"type": "object"}}}, Timestamp: 14}
	history := []Message{first, UserMessage{Content: UserText("hello"), Timestamp: 11}, also, AssistantMessage{Content: []AssistantContentBlock{TextContent{Text: "ok"}}, Timestamp: 13}, last}

	var plain TranscriptMessages[Message] = history
	custom := TranscriptMessages[appRoleMessage]{
		{role: "system", system: &first}, {role: "bashExecution"}, {role: "user"}, {role: "system", system: &also},
		{role: "assistant"}, {role: "branchSummary"}, {role: "system", system: &last},
	}

	const wantPrompt = "base\n\nalso do this\n\n<a>2</a>\n\n<c>1</c>"
	check := func(name string, current *SystemMessage, prompt string, tools []ToolSchema) {
		t.Helper()
		if current == nil {
			t.Fatalf("%s: no current system message", name)
		}
		if got := GetCurrentSystemPrompt([]Message{*current}); got != "base\n\nalso do this\n\n<a>2</a>\n\n<c>1</c>" {
			t.Errorf("%s: replayed message prompt = %q", name, got)
		}
		if current.Timestamp != 10 {
			t.Errorf("%s: timestamp = %d, want 10 (the first system message's)", name, current.Timestamp)
		}
		if names := toolNames(current.ToolsAdded); !reflect.DeepEqual(names, []string{"second"}) {
			t.Errorf("%s: toolsAdded = %v, want [second]", name, names)
		}
		if prompt != wantPrompt {
			t.Errorf("%s: GetCurrentSystemPrompt = %q, want %q", name, prompt, wantPrompt)
		}
		if names := toolNames(tools); !reflect.DeepEqual(names, []string{"second"}) {
			t.Errorf("%s: GetCurrentTools = %v, want [second]", name, names)
		}
	}
	check("TranscriptMessages[Message]", GetCurrentSystemMessage(plain), GetCurrentSystemPrompt(plain), GetCurrentTools(plain))
	check("app-defined roles", GetCurrentSystemMessage(custom), GetCurrentSystemPrompt(custom), GetCurrentTools(custom))
}

func toolNames(tools []ToolSchema) []string {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
	}
	return names
}
