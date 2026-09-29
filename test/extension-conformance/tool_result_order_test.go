package extensionconformance

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// All four existing rich_tool fixtures return exactly this array; the assertion must not project text and images separately.
func assertOrderedRichToolResult(t *testing.T, rich agent.AgentToolResult) {
	t.Helper()
	want := []ai.ToolResultMessageContent{ai.TextContent{Text: "  padded  "}, ai.ImageContent{Data: "aW1n", MimeType: "image/png"}, ai.TextContent{Text: "tail\n"}}
	if !reflect.DeepEqual(rich.Content, want) {
		t.Fatalf("rich_tool ordered content = %#v, want %#v", rich.Content, want)
	}
}

func TestPackedOrderedToolResults(t *testing.T) {
	for _, language := range []string{"go", "rust", "python"} {
		t.Run(language, func(t *testing.T) {
			h := makePackedUIHarness(t, language)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			tool, ok := findTool(h.runner, "rich_tool")
			if !ok {
				t.Fatal("rich_tool missing")
			}
			result, err := tool.Definition.Execute(t.Context(), "ordered", json.RawMessage(`{}`), nil)
			if err != nil {
				t.Fatal(err)
			}
			rich, ok := result.(agent.AgentToolResult)
			if !ok {
				t.Fatalf("rich result type %T", result)
			}
			assertOrderedRichToolResult(t, rich)
		})
	}
}
