package ai

import "testing"

type customRoleMessage struct{ role string }

func (m customRoleMessage) MessageRole() string { return m.role }

// utils/transcript.ts:35-37,40 TranscriptMessages is `readonly { role: string }[]`: the replay helpers read only entries whose role is "system", so a transcript that carries application-defined message roles can be passed without filtering.
func TestTranscriptHelpersAcceptAnyRoleCarryingMessage(t *testing.T) {
	system := SystemMessage{Content: SystemText("lead"), ToolsAdded: []ToolSchema{{Name: "read"}}}
	mixed := []RoleMessage{system, customRoleMessage{"notification"}, customRoleMessage{"system"}}
	if got := GetInitialSystemMessage(mixed); got == nil || got.Content != SystemText("lead") {
		t.Fatalf("leading system message = %+v", got)
	}
	if tools := GetCurrentTools(mixed); len(tools) != 1 || tools[0].Name != "read" {
		t.Fatalf("current tools = %+v", tools)
	}
	if got := GetCurrentSystemPrompt(mixed); got != "lead" {
		t.Fatalf("current prompt = %q", got)
	}
	if got := GetInitialSystemMessage([]RoleMessage{customRoleMessage{"notification"}, system}); got != nil {
		t.Fatalf("a later system message is not the initial one: %+v", got)
	}
	if got := GetInitialSystemMessage([]*SystemMessage{&system}); got == nil {
		t.Fatal("a pointer to a system message is a system message")
	}
}
