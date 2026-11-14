package codingagent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// pi: agent-session.ts:672-678 hands the content the tool_result handlers returned to the agent loop as the objects they are, so a text with
// a lone UTF-16 surrogate (a tool's own result that a handler passed on, or a block a handler added) is in the tool result message with that
// unit. PiG reads a block that is not one of its own types through its JSON form: that form writes the unit as its escape and reads it back.
func TestToolResultEventOverrideKeepsALoneSurrogate(t *testing.T) {
	const unit = "\xed\xa0\xbd"
	result := ToolResultEventOverride(&extension.ToolResultEventResult{Content: []any{
		map[string]any{"type": "text", "text": "TR[" + unit + "]"},
		map[string]any{"type": "text", "text": "TX[" + unit + "]", "textSignature": "S[" + unit + "]"},
		map[string]any{"type": "image", "data": "D[" + unit + "]", "mimeType": "image/png"},
	}})
	if len(result.Content) != 3 {
		t.Fatalf("the override holds %d blocks, want 3: %#v", len(result.Content), result.Content)
	}
	if block, ok := result.Content[0].(ai.TextContent); !ok || block.Text != "TR["+unit+"]" {
		t.Errorf("the first block = %#v, want the text with the unit", result.Content[0])
	}
	if block, ok := result.Content[1].(ai.TextContent); !ok || block.Text != "TX["+unit+"]" || block.TextSignature != "S["+unit+"]" {
		t.Errorf("the second block = %#v, want the text and the signature with the unit", result.Content[1])
	}
	if block, ok := result.Content[2].(ai.ImageContent); !ok || block.Data != "D["+unit+"]" {
		t.Errorf("the third block = %#v, want the data with the unit", result.Content[2])
	}
}
