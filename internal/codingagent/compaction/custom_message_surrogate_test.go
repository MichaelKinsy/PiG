package compaction

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// messages.ts convertToLlm hands a custom message's content to the model as the string or the blocks the extension wrote, so a lone UTF-16 unit in it
// is the unit the provider request carries, not U+FFFD from a round trip through encoding/json.
func TestCustomMessageToLlmKeepsALoneSurrogate(t *testing.T) {
	lone := jsstring.FromUTF16([]uint16{0xd800})
	for name, content := range map[string]any{
		"decoded blocks":  []any{map[string]any{"type": "text", "text": "a" + lone}},
		"wire blocks":     json.RawMessage(`[{"type":"text","text":"a\ud800"}]`),
		"typed blocks":    ai.UserContentBlocks{ai.TextContent{Text: "a" + lone}},
		"wire string":     json.RawMessage(`"a\ud800"`),
		"decoded string":  "a" + lone,
		"typed text blob": ai.UserText("a" + lone),
	} {
		t.Run(name, func(t *testing.T) {
			got := convertToLlm([]agent.AgentMessage{{Custom: map[string]any{"role": agent.RoleCustom, "content": content}}})
			if len(got) != 1 {
				t.Fatalf("converted %d messages, want 1", len(got))
			}
			blocks, ok := got[0].(ai.UserMessage).Content.(ai.UserContentBlocks)
			if !ok || len(blocks) != 1 {
				t.Fatalf("content = %#v, want one text block", got[0].(ai.UserMessage).Content)
			}
			if text := blocks[0].(ai.TextContent).Text; text != "a"+lone {
				t.Errorf("text units = %x, want %x", jsstring.ToUTF16(text), jsstring.ToUTF16("a"+lone))
			}
		})
	}
}
