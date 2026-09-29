package coding

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// AgentSession._rebuildSystemPrompt passes an explicit selectedTools array, even when every tool is deactivated.
func TestDeactivatingAllToolsDoesNotRestoreDefaultPromptTools(t *testing.T) {
	var prompt string
	var toolCount int
	h := newRecoveryHarness(t, harnessOptions{tools: []agent.AgentTool{&fakeTool{name: "echo"}}}, func(messages []ai.Message) *ai.AssistantMessage {
		prompt = ai.GetCurrentSystemPrompt(messages)
		toolCount = len(ai.GetCurrentTools(messages))
		return fauxReply("done", ai.StopReasonStop, 0)(messages)
	})
	h.session.SetActiveToolsByName(nil)
	if _, err := h.session.Send(t.Context(), "Test"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "<tools>\n(none)\n") || strings.Contains(prompt, "- bash:") || strings.Contains(prompt, "Use bash for file operations") || toolCount != 0 {
		t.Fatalf("empty tool selection: tools=%d prompt=%s", toolCount, prompt)
	}
}
