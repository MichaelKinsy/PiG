package export

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi's export renders a stored toolResult with `{ content, details: msg.details, isError }`
// (export-html/tool-renderer.ts renderResult, export-html/index.ts:206-226), so a renderer sees a stored details: null
// as null and an absent details as undefined.
func TestRenderCustomToolsKeepsStoredNullDetails(t *testing.T) {
	for _, tc := range []struct {
		name    string
		message string
		null    bool
		details any
	}{
		{"explicit null", `{"role":"toolResult","toolCallId":"call-1","toolName":"custom-tool","content":[],"details":null,"isError":false}`, true, nil},
		{"absent", `{"role":"toolResult","toolCallId":"call-1","toolName":"custom-tool","content":[],"isError":false}`, false, nil},
		{"value", `{"role":"toolResult","toolCallId":"call-1","toolName":"custom-tool","content":[],"details":{"n":1},"isError":false}`, false, map[string]any{"n": float64(1)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sd := &SessionData{
				Header:  mustRaw(map[string]any{"type": "session", "id": "test", "cwd": "/tmp"}),
				Entries: []json.RawMessage{json.RawMessage(`{"type":"message","id":"r1","message":` + tc.message + `}`)},
			}
			var seen []agent.AgentToolResult
			tools := []extension.RegisteredTool{{
				Definition: extension.ToolDefinition{
					Name: "custom-tool",
					RenderResult: func(result extension.AgentToolResult, _ extension.ToolRenderResultOptions, _ extension.Theme, _ extension.ToolRenderContext) extension.Component {
						seen = append(seen, result.(agent.AgentToolResult))
						return testComponent{lines: []string{"RESULT"}}
					},
				},
			}}
			RenderCustomTools(sd, tools, "/tmp", 80)
			if len(seen) == 0 {
				t.Fatal("renderResult was not called")
			}
			for _, result := range seen {
				if result.DetailsNull() != tc.null {
					t.Fatalf("DetailsNull %t, want %t (%#v)", result.DetailsNull(), tc.null, result)
				}
				got, _ := json.Marshal(result.Details)
				want, _ := json.Marshal(tc.details)
				if string(got) != string(want) {
					t.Fatalf("details %s, want %s", got, want)
				}
			}
		})
	}
}
