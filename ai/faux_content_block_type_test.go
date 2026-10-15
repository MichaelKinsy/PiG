package ai

import "testing"

// faux.ts FauxContentBlock = TextContent | ThinkingContent | ToolCall: the helpers build blocks whose type is one of the three literals.
func TestFauxContentBlockHelpersBuildTheUpstreamTypes(t *testing.T) {
	for block, want := range map[FauxContentBlockType]FauxContentBlock{
		"text":     FauxText("a"),
		"thinking": FauxThinking("b"),
		"toolCall": FauxToolCall("tool", map[string]any{}, &FauxToolCallOptions{ID: "id"}),
	} {
		if want.Type != block {
			t.Errorf("block type %q, want %q", want.Type, block)
		}
	}
	if FauxContentText != "text" || FauxContentThinking != "thinking" || FauxContentToolCall != "toolCall" {
		t.Error("FauxContentBlockType constants differ from the faux.ts literals")
	}
}
