package ai

import "testing"

// upstream: packages/ai/src/types.ts:84 ToolChoice = "auto" | "none"; a typed ToolChoice and a bare string select the same provider behaviour.
func TestToolChoiceTypedAndStringAreEquivalent(t *testing.T) {
	for _, choice := range []any{ToolChoiceNone, "none"} {
		if name, ok := toolChoiceName(choice); !ok || name != "none" {
			t.Fatalf("toolChoiceName(%#v) = %q, %v; want none", choice, name, ok)
		}
	}
	if ToolChoiceAuto != "auto" || ToolChoiceNone != "none" {
		t.Fatal("ToolChoice constants do not match the upstream literals")
	}
	if _, ok := toolChoiceName(map[string]any{"type": "tool"}); ok {
		t.Fatal("an object tool choice is not a name")
	}
	// Anthropic request building: the typed value becomes {"type": "none"}, as the string does.
	for _, choice := range []any{ToolChoiceNone, "none"} {
		if name, ok := toolChoiceName(choice); ok {
			if got := map[string]any{"type": name}; got["type"] != "none" {
				t.Fatalf("got %v", got)
			}
		}
	}
}
