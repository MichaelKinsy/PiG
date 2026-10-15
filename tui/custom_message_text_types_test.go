package tui

import "testing"

// Pi: packages/coding-agent/src/modes/interactive/components/custom-message.ts:103-105 `.filter((c): c is TextContent => c.type === "text").map((c) => c.text).join("\n")`.
// Only blocks of type "text" contribute: a block of any other type that carries a text member (a thinking block, an unknown type, an empty type) is left out, not only image blocks.
// mutation-checked: the mutant "every block that is not an image contributes" (custom_message.go:158) fails it.
func TestCustomMessageTextKeepsOnlyBlocksOfTypeText(t *testing.T) {
	content := []any{
		map[string]any{"type": "text", "text": "kept"},
		map[string]any{"type": "thinking", "text": "hidden reasoning"},
		map[string]any{"type": "audio", "text": "transcript"},
		map[string]any{"text": "no type"},
		map[string]any{"type": "Text", "text": "wrong case"},
		map[string]any{"type": "text", "text": "also kept"},
	}
	if got, want := CustomMessageText(&CustomMessage{CustomType: "x", Content: content}), "kept\nalso kept"; got != want {
		t.Fatalf("CustomMessageText = %q, want %q", got, want)
	}
}
