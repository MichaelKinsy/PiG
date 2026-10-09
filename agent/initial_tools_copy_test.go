package agent

import "testing"

// Pi packages/agent/src/agent.ts:83 copies the initial executable tool array.
// Mutating the caller's array must not change the live loadout independently of
// the transcript declaration captured by the constructor.
func TestAgentCopiesInitialToolArray(t *testing.T) {
	tools := []AgentTool{&scriptTool{name: "first"}}
	a := mustNewAgent(AgentOptions{Tools: tools})
	tools[0] = &scriptTool{name: "second"}
	if a.Tools()[0].Name() != "first" || a.Messages()[0].System.ToolsAdded[0].Name != "first" {
		t.Fatal("caller mutated the initial executable loadout")
	}
}
