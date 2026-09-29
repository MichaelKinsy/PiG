package codingagent

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi's tool_result content includes an empty text block, unlike content:[] or image-only content.
func TestToolResultEmptyTextHookRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		name    string
		result  agent.AgentToolResult
		want    []any
		present bool
	}{
		{"empty text", agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: ""}}}, []any{map[string]any{"type": "text", "text": ""}}, true},
		{"empty array", agent.AgentToolResult{}, []any{}, false},
		{"image only", agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.ImageContent{Data: "aW1n", MimeType: "image/png"}}}, []any{map[string]any{"type": "image", "data": "aW1n", "mimeType": "image/png"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			content := ToolResultEventContent(tc.result)
			if !reflect.DeepEqual(content, tc.want) {
				t.Fatalf("event content = %#v, want %#v", content, tc.want)
			}
			if got := extensionToolResult(tc.result)["content"]; !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("execution-end content = %#v, want %#v", got, tc.want)
			}
			override := ToolResultEventOverride(&extension.ToolResultEventResult{Content: content})
			present := false
			for _, block := range override.Content {
				if _, ok := block.(ai.TextContent); ok {
					present = true
				}
			}
			if present != tc.present || !reflect.DeepEqual(ToolResultEventContent(agent.AgentToolResult{Content: override.Content}), tc.want) {
				t.Fatalf("override lost text presence: %#v", override)
			}
		})
	}
}

func TestToolResultExtensionContentPreservesAndClearsImages(t *testing.T) {
	original := agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "before"}, ai.ImageContent{Data: "original", MimeType: "image/png"}}}
	content := ToolResultEventContent(original)
	if len(content) != 2 || content[1].(map[string]any)["data"] != "original" {
		t.Fatalf("extension content=%#v", content)
	}
	override := ToolResultEventOverride(&extension.ToolResultEventResult{Content: []any{ai.TextContent{Text: "after"}, map[string]any{"type": "image", "data": "replacement", "mimeType": "image/jpeg"}}, IsError: new(true)})
	if !reflect.DeepEqual(override.Content, []ai.ToolResultMessageContent{ai.TextContent{Text: "after"}, ai.ImageContent{Data: "replacement", MimeType: "image/jpeg"}}) || override.IsError == nil || !*override.IsError {
		t.Fatalf("override=%#v", override)
	}
	cleared := ToolResultEventOverride(&extension.ToolResultEventResult{Content: []any{map[string]any{"type": "text", "text": "only text"}}})
	if !reflect.DeepEqual(cleared.Content, []ai.ToolResultMessageContent{ai.TextContent{Text: "only text"}}) {
		t.Fatalf("text-only replacement retained images=%#v", cleared.Content)
	}
}

// GUARD-15: upstream keeps a tool_result handler's blocks as they are, so a
// text or image block whose fields are not strings still reaches the model
// (read with JavaScript string coercion) instead of vanishing.
func TestToolResultEventOverridePreservesNonStringFields(t *testing.T) {
	override := ToolResultEventOverride(&extension.ToolResultEventResult{Content: []any{
		map[string]any{"type": "text", "text": 42.0},
		map[string]any{"type": "text", "text": true},
		json.RawMessage(`{"type":"text","text":{"nested":1}}`),
		map[string]any{"type": "image", "data": "abc", "mimeType": 7.0},
	}})
	want := []ai.ToolResultMessageContent{ai.TextContent{Text: "42"}, ai.TextContent{Text: "true"}, ai.TextContent{Text: "[object Object]"}, ai.ImageContent{Data: "abc", MimeType: "7"}}
	if !reflect.DeepEqual(override.Content, want) {
		t.Fatalf("content = %#v, want %#v", override.Content, want)
	}
}

func TestToolResultEventOverrideCarriesUsage(t *testing.T) {
	override := ToolResultEventOverride(&extension.ToolResultEventResult{Usage: map[string]any{"input": float64(3), "output": float64(4)}})
	if override.Usage == nil || override.Usage.Input != 3 || override.Usage.Output != 4 {
		t.Fatalf("usage override = %+v", override.Usage)
	}
	if ToolResultEventOverride(&extension.ToolResultEventResult{}).Usage != nil {
		t.Fatal("an absent usage must keep the tool's own usage")
	}
}
