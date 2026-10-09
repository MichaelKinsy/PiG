package coding

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi 1.0.4 passes `hiddenTools: [...this._hiddenDeclarations]` to the system prompt options (agent-session.ts
// _rebuildSystemPrompt), and _applyToolLoadout fills that Set in hook order, then in each hook's hiddenDeclarations
// order. Extensions read the list through getSystemPromptOptions() and before_agent_start, so its order is the hooks'
// order, not a sorted one.
// Pi: packages/coding-agent/src/core/system-prompt.ts:20 (BuildSystemPromptOptions.hiddenTools).
func TestSystemPromptOptionsListHiddenToolsInTheOrderTheHooksHidThem(t *testing.T) {
	noop := func(context.Context, string, json.RawMessage, extension.AgentToolUpdateCallback) (extension.AgentToolResult, error) {
		return textResult("ok"), nil
	}
	zeta := orchestrationTool("zeta", "Zeta.", extension.ToolDefinition{Execute: noop})
	alpha := orchestrationTool("alpha", "Alpha.", extension.ToolDefinition{Execute: noop})
	hider := orchestrationTool("hider", "Hides tools.", extension.ToolDefinition{
		Exposure: extension.ToolExposureModelOnly,
		Execute:  noop,
		PrepareLoadout: func(extension.ToolLoadout) *extension.ToolLoadoutChanges {
			return &extension.ToolLoadoutChanges{HiddenDeclarations: []string{"zeta", "alpha", "zeta"}}
		},
	})
	session, _ := newOrchestrationSession(t, extension.Extension{
		Tools:     map[string]extension.RegisteredTool{"zeta": zeta, "alpha": alpha, "hider": hider},
		ToolOrder: []string{"zeta", "alpha", "hider"},
	})
	session.SetActiveToolsByName([]string{"zeta", "alpha", "hider"})

	want := []string{"zeta", "alpha"}
	if got := session.HiddenDeclarationNames(); !slices.Equal(got, want) {
		t.Fatalf("hidden declarations = %q, want %q", got, want)
	}
	if got := session.buildSystemPromptOptions(session.ActiveToolNames()).HiddenTools; !slices.Equal(got, want) {
		t.Fatalf("system prompt options hiddenTools = %q, want %q", got, want)
	}
}
