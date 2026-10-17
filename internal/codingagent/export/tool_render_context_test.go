package export

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// export-html/tool-renderer.ts createRenderContext builds the context a custom renderer sees in an export:
// `durationMs: undefined, outputPad: 1`. A stored result's own durationMs is not passed through, and the padding is the
// default 1 rather than the interactive setting.
func TestRenderCustomToolsGivesRenderersTheExportContext(t *testing.T) {
	sd := &SessionData{
		Header: mustRaw(map[string]any{"type": "session", "id": "test", "cwd": "/tmp"}),
		Entries: []json.RawMessage{json.RawMessage(
			`{"type":"message","id":"r1","message":{"role":"toolResult","toolCallId":"call-1","toolName":"custom-tool","content":[],"isError":false,"durationMs":42}}`)},
	}
	var seen []extension.ToolRenderContext
	tools := []extension.RegisteredTool{{
		Definition: extension.ToolDefinition{
			Name: "custom-tool",
			RenderResult: func(_ extension.AgentToolResult, _ extension.ToolRenderResultOptions, _ extension.Theme, ctx extension.ToolRenderContext) extension.Component {
				seen = append(seen, ctx)
				return testComponent{lines: []string{"RESULT"}}
			},
		},
	}}
	RenderCustomTools(sd, ToolRenderersOf(tools), "/tmp", 80)
	if len(seen) == 0 {
		t.Fatal("renderResult was not called")
	}
	for _, ctx := range seen {
		if ctx.OutputPad != 1 {
			t.Fatalf("OutputPad = %d, want 1", ctx.OutputPad)
		}
		if ctx.DurationMs != nil {
			t.Fatalf("DurationMs = %d, want absent", *ctx.DurationMs)
		}
	}
}
