package agent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi: packages/ai/src/utils/transcript.ts:40 (TranscriptMessages = readonly { role: string }[]). An agent transcript with custom roles
// (bash execution, branch summary) reaches the transcript helpers as it is: AgentMessage is a RoleMessage and the helpers read the
// system messages it wraps, with no pre-filtering to []ai.Message.
func TestAgentTranscriptWithCustomRolesReachesTheTranscriptHelpers(t *testing.T) {
	first := ai.SystemMessage{Content: ai.SystemText("base"), ToolsAdded: []ai.ToolSchema{{Name: "read"}}}
	later := ai.SystemMessage{ToolsAdded: []ai.ToolSchema{{Name: "write"}}}
	transcript := []AgentMessage{
		{System: &first},
		{Custom: map[string]any{"role": "bashExecution", "command": "ls"}},
		{System: &later},
	}
	tools := ai.GetCurrentTools(transcript)
	if len(tools) != 2 || tools[0].Name != "read" || tools[1].Name != "write" {
		t.Fatalf("GetCurrentTools(agent transcript) = %+v, want read then write", tools)
	}
	if got := transcript[1].MessageRole(); got != "bashExecution" {
		t.Fatalf("custom role = %q", got)
	}
	if _, ok := transcript[1].AsSystemMessage(); ok {
		t.Fatal("a custom-role message is not a system message")
	}
}
