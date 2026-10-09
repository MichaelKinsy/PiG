package codemode

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// upstream: tool.ts createCodemodeToolDefinition ends with `...codemodeRenderers`, so the registered definition draws
// its own card; the interactive mode has no codemode renderer keyed by tool name to fall back to.
func TestTheRegisteredDefinitionCarriesTheCodemodeRenderers(t *testing.T) {
	ext, err := loadExtension(Options{})
	if err != nil {
		t.Fatal(err)
	}
	definition := registeredDefinition(ext)
	if definition.RenderCall == nil || definition.RenderResult == nil {
		t.Fatal("the codemode definition has no renderers")
	}
	render := func(component extension.Component) string {
		return strings.Join(component.(interface{ Render(int) []string }).Render(80), "\n")
	}
	if call := render(definition.RenderCall(json.RawMessage(`{"code":"return 1"}`), nil, extension.ToolRenderContext{})); !strings.Contains(call, "return") {
		t.Errorf("renderCall = %q, want the script", call)
	}
	result := agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "Script completed\nWall time 0.1 seconds\nOutput:\n"}, ai.TextContent{Text: "hello"}}}
	if got := render(definition.RenderResult(result, extension.ToolRenderResultOptions{}, nil, extension.ToolRenderContext{})); strings.Contains(got, "Script completed") || !strings.Contains(got, "hello") {
		t.Errorf("renderResult = %q, want the output without the header", got)
	}
}
