package codingagent

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

func TestToolResultExtensionContentPreservesAndClearsImages(t *testing.T) {
	original := agent.AgentToolResult{Content: "before", Images: []ai.ImageContent{{Data: "original", MimeType: "image/png"}}}
	content := ToolResultEventContent(original)
	if len(content) != 2 || content[1].(map[string]any)["data"] != "original" {
		t.Fatalf("extension content=%#v", content)
	}
	override := ToolResultEventOverride(&extension.ToolResultEventResult{Content: []any{ai.TextContent{Text: "after"}, map[string]any{"type": "image", "data": "replacement", "mimeType": "image/jpeg"}}, IsError: new(true)})
	if override.Content == nil || *override.Content != "after" || override.Images == nil || len(*override.Images) != 1 || (*override.Images)[0].Data != "replacement" || override.IsError == nil || !*override.IsError {
		t.Fatalf("override=%#v", override)
	}
	cleared := ToolResultEventOverride(&extension.ToolResultEventResult{Content: []any{map[string]any{"type": "text", "text": "only text"}}})
	if cleared.Images == nil || len(*cleared.Images) != 0 {
		t.Fatalf("text-only replacement retained images=%#v", cleared.Images)
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
	if got := *override.Content; got != "42true[object Object]" {
		t.Fatalf("content = %q", got)
	}
	if images := *override.Images; len(images) != 1 || images[0].Data != "abc" || images[0].MimeType != "7" {
		t.Fatalf("images = %#v", images)
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
