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

// A tool hands the agent the object it built, and Pi's events and JSON stream carry that object (agent-loop.ts:778-786, 912-919), so its members keep the order the tool wrote them in: {details, isError, content} here, not the declared content, details, isError. Node's runtime used to rebuild the object in a fixed order; the host now reads the order from the wire object every SDK sends, so a Python dict and a Rust JSON object keep theirs too. The Go SDK builds the wire object from a struct, which has one fixed order (a Go language mechanic), and the in-process reference builds agent.AgentToolResult directly, so neither is a row.
func TestToolResultMemberOrderFollowsTheToolAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	want := []string{"details", "isError", "content"}
	wantPartial := []string{"details", "content"}
	for _, tc := range allHarnessCases() {
		switch tc.name {
		case "subprocess-node", "subprocess-node-packed", "subprocess-rust", "subprocess-python":
		default:
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			tool, ok := findTool(h.runner, "ordered_result")
			if !ok {
				t.Fatal("ordered_result not registered")
			}
			var partials []agent.AgentToolResult
			var onUpdate agent.ToolUpdateCallback = func(partial agent.AgentToolResult) { partials = append(partials, partial) }
			result, err := tool.Definition.Execute(t.Context(), "tc-order", json.RawMessage(`{}`), onUpdate)
			if err != nil {
				t.Fatal(err)
			}
			final, _ := result.(agent.AgentToolResult)
			if !reflect.DeepEqual(final.MemberOrder, want) {
				t.Errorf("result member order = %v, want %v", final.MemberOrder, want)
			}
			if len(partials) != 1 || !reflect.DeepEqual(partials[0].MemberOrder, wantPartial) {
				t.Errorf("partials = %#v, want one with member order %v", partials, wantPartial)
			}
		})
	}
}
