package agent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// pi: coding-agent core/messages.ts convertToLlm sends the content array of a custom message as the array it is, so a text block with a lone
// UTF-16 surrogate (an extension sent it, or a session line holds it) is in the user message with that unit. PiG reads an array that is not
// of its own block types through its JSON form: that form writes the unit as its escape and reads it back.
func TestCustomMessageBlocksKeepALoneSurrogate(t *testing.T) {
	const unit = "\xed\xa0\xbd"
	user, ok := customUserMessage(map[string]any{"role": RoleCustom, "content": []any{map[string]any{"type": "text", "text": "CB[" + unit + "]"}}, "timestamp": int64(1)})
	blocks, _ := user.Content.(ai.UserContentBlocks)
	if !ok || len(blocks) != 1 {
		t.Fatalf("the user message = %#v, want one text block", user)
	}
	if text, _ := blocks[0].(ai.TextContent); text.Text != "CB["+unit+"]" {
		t.Errorf("the block = %#v, want the text with the unit", blocks[0])
	}
}
