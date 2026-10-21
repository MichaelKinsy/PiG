package coding

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

func activatedToolStub(t *testing.T, name, snippet string) agent.AgentTool {
	t.Helper()
	tool, err := newBridgeTool(extension.RegisteredTool{Definition: extension.ToolDefinition{Name: name, Label: name, Description: name + " tool", PromptSnippet: snippet, Parameters: json.RawMessage(`{"type":"object","properties":{}}`), Execute: func(context.Context, string, json.RawMessage, extension.AgentToolUpdateCallback) (extension.AgentToolResult, error) {
		return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "ok"}}}, nil
	}}})
	if err != nil {
		t.Fatal(err)
	}
	return tool
}

// setActiveToolsByName rebuilds the base prompt options (agent-session.ts _rebuildSystemPrompt) and the next run's
// _prepareRun diffs them against the transcript, so the run a triggerTurn message starts lists a tool activated after the
// last run, as it does the tool request's declarations.
func TestCustomMessageRunListsToolsActivatedSinceTheLastRun(t *testing.T) {
	var prompts []string
	var declared [][]string
	record := func(request []ai.Message) *ai.AssistantMessage {
		prompts = append(prompts, ai.GetCurrentSystemPrompt(request))
		var names []string
		for _, tool := range ai.GetCurrentTools(request) {
			names = append(names, tool.Name)
		}
		declared = append(declared, names)
		return fauxReply("done", ai.StopReasonStop, 0)(request)
	}
	h := newRecoveryHarness(t, harnessOptions{tools: []agent.AgentTool{
		activatedToolStub(t, "base_tool", "Run base_tool first."),
		activatedToolStub(t, "late_tool", "Run late_tool when the late work is due."),
	}}, record, record)
	h.session.SetActiveToolsByName([]string{"base_tool"})
	if err := h.session.Prompt(t.Context(), "start"); err != nil {
		t.Fatal(err)
	}
	h.session.SetActiveToolsByName([]string{"base_tool", "late_tool"})
	if err := h.session.SendCustomMessage(t.Context(), extension.CustomMessageRef{CustomType: "again", Content: "continue", Display: true}, &extension.SendMessageOptions{TriggerTurn: new(true)}); err != nil {
		t.Fatal(err)
	}
	if len(prompts) != 2 {
		t.Fatalf("requests = %d, want 2", len(prompts))
	}
	if strings.Contains(prompts[0], "late_tool") {
		t.Fatalf("the first request lists the inactive tool: %q", prompts[0])
	}
	if !strings.Contains(strings.Join(declared[1], ","), "late_tool") {
		t.Fatalf("the second request declares %v, want late_tool", declared[1])
	}
	if !strings.Contains(prompts[1], "- late_tool: Run late_tool when the late work is due.") {
		t.Fatalf("the custom-message run's system prompt lacks the activated tool's snippet: %q", prompts[1])
	}
}
