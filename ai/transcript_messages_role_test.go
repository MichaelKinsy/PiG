package ai

import "testing"

type appRoleMessage struct {
	role   string
	system *SystemMessage
}

func (m appRoleMessage) MessageRole() string { return m.role }
func (m appRoleMessage) AsSystemMessage() (SystemMessage, bool) {
	if m.system == nil {
		return SystemMessage{}, false
	}
	return *m.system, true
}

// Pi: packages/ai/src/utils/transcript.ts:40 (TranscriptMessages = readonly { role: string }[]) and its doc comment: the replay
// helpers read only the entries whose role is "system", so a transcript that carries app-defined message roles is passed without
// filtering. A slice of app-defined role values reaches every helper and the system entries are the ones read.
func TestTranscriptHelpersReadAnyMessageListThatNamesItsRole(t *testing.T) {
	first := SystemMessage{Content: SystemText("base"), ToolsAdded: []ToolSchema{{Name: "read"}}}
	later := SystemMessage{Content: SystemText("later"), ToolsAdded: []ToolSchema{{Name: "write"}}, ToolsRemoved: []ToolReference{{Name: "read"}}}
	transcript := []appRoleMessage{
		{role: "system", system: &first},
		{role: "bashExecution"},
		{role: "system", system: &later},
		{role: "branchSummary"},
	}
	if initial := GetInitialSystemMessage(transcript); initial == nil || GetCurrentSystemPrompt([]Message{*initial}) != "base" {
		t.Fatalf("GetInitialSystemMessage = %+v, want the leading system message", initial)
	}
	tools := GetCurrentTools(transcript)
	if len(tools) != 1 || tools[0].Name != "write" {
		t.Fatalf("GetCurrentTools = %+v, want the deltas applied in order over the custom roles", tools)
	}
	if got := GetCurrentSystemPrompt(transcript); got != "base\n\nlater" {
		t.Fatalf("GetCurrentSystemPrompt = %q, want both system entries", got)
	}
	if GetInitialSystemMessage([]appRoleMessage{{role: "user"}, {role: "system", system: &first}}) != nil {
		t.Fatal("a transcript that does not start with a system message has no initial one")
	}
}
