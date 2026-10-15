package coding

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi 1.2.0 types.ts ToolInfo picks reachesTools from ToolDefinition, and agent-session.ts getAllTools copies a definition's
// reachesTools onto its ToolInfo (`...(definition.reachesTools ? { reachesTools: definition.reachesTools } : {})`), so an
// extension's tool_call handler can ask which tools a call may reach. A tool without the hook has none.
func TestGetAllToolsCarriesTheDefinitionsReachesTools(t *testing.T) {
	execute := func(context.Context, string, json.RawMessage, extension.AgentToolUpdateCallback) (extension.AgentToolResult, error) {
		return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "ok"}}}, nil
	}
	finder := extension.ToolDefinition{
		Name: "finder", Label: "finder", Description: "Finds tools.", Parameters: json.RawMessage(`{"type":"object"}`), Execute: execute,
		ReachesTools: func(input json.RawMessage) extension.ToolReach {
			return extension.ToolReach{Words: []string{"reached:" + string(input)}}
		},
	}
	plain := extension.ToolDefinition{Name: "plain", Label: "plain", Description: "Reaches nothing.", Parameters: json.RawMessage(`{"type":"object"}`), Execute: execute}
	ext := extension.Extension{Name: "reach", Path: "<inline:reach>", ResolvedPath: "<inline:reach>",
		Handlers: map[string][]extension.HandlerFn{},
		Tools:    map[string]extension.RegisteredTool{finder.Name: {Definition: finder}, plain.Name: {Definition: plain}}, ToolOrder: []string{finder.Name, plain.Name}}
	h := newRecoveryHarness(t, harnessOptions{extension: ext})

	infos := map[string]extension.ToolInfo{}
	for _, info := range h.session.GetAllTools() {
		infos[info.Name] = info
	}
	got, ok := infos["finder"]
	if !ok {
		t.Fatalf("getAllTools lacks finder: %v", infos)
	}
	if got.ReachesTools == nil {
		t.Fatal("finder's ToolInfo has no reachesTools")
	}
	if reach := got.ReachesTools(json.RawMessage(`{"q":1}`)); reach.All || !slices.Equal(reach.Words, []string{`reached:{"q":1}`}) {
		t.Fatalf("finder's reachesTools = %+v, want the definition's hook", reach)
	}
	if infos["plain"].ReachesTools != nil {
		t.Error("a tool without reachesTools has one in getAllTools")
	}
}
