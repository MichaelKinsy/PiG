package ai

import "testing"

func TestCloneAssistantContentPreservesNilToolArguments(t *testing.T) {
	// Pi normalizeContext leaves tool arguments intact; provider converters own any null-to-empty-object fallback.
	original := []AssistantContentBlock{ToolCall{ID: "nil", Name: "lookup"}, ToolCall{ID: "nested", Name: "lookup", Arguments: JsonObject{"nested": map[string]any{"value": "original"}}}}
	cloned := cloneAssistantContent(original)
	if cloned[0].(ToolCall).Arguments != nil {
		t.Fatalf("nil arguments changed: %+v", cloned[0])
	}
	cloned[1].(ToolCall).Arguments["nested"].(map[string]any)["value"] = "changed"
	if got := original[1].(ToolCall).Arguments["nested"].(map[string]any)["value"]; got != "original" {
		t.Fatalf("clone changed retained arguments: %v", got)
	}
}
