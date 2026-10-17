package harness

import (
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
)

// Pi packages/durable/src/harness/events.ts:28-33 (MessageChange) and messageChanges(), events.ts:352-385: the view operations on pi.live.generation.message become relative
// message changes. A block spliced in whole yields <kind>_start per block; an append to a block's text, thinking or tool-call arguments yields a delta; any other change to a
// block sends the block whole once, and later operations on that index in the batch add nothing; usage operations and operations outside the partial add nothing; a change that
// is not a content append at index 0 (a splice that removes, a non-content field, a replaced ancestor) sends the whole message.
// mutation-checked: dropping the whole-block dedup, the usage skip, the delete-count guard, or the field or path-length test of the append cases each fails a case.
func TestMessageChangesFollowPiViewOperationRules(t *testing.T) {
	partial := func(rest ...any) []any { return append([]any{"docs", "pi.live", "generation", "message"}, rest...) }
	text := map[string]any{"type": "text", "text": "hi"}
	thinking := map[string]any{"type": "thinking", "thinking": "hm"}
	call := map[string]any{"type": "toolCall", "id": "c", "name": "n", "arguments": map[string]any{}}
	cases := []struct {
		name string
		ops  []durable.Op
		want string
	}{
		{"text append", []durable.Op{{"a", partial("content", 0, "text"), " there"}}, "text_delta@0[ there]"},
		{"thinking append", []durable.Op{{"a", partial("content", 2, "thinking"), "x"}}, "thinking_delta@2[x]"},
		{"tool-call argument append keeps its path", []durable.Op{{"a", partial("content", 1, "arguments", "a", 0), `"v`}}, `toolcall_delta@1[a/0:"v]`},
		{"spliced blocks start by block type", []durable.Op{{"p", partial("content"), 0, 0, []any{text, thinking, call}}}, "text_start@0 thinking_start@1 toolcall_start@2"},
		{"splice offsets by its index", []durable.Op{{"p", partial("content"), 3, 0, []any{text}}}, "text_start@3"},
		{"a splice that removes sends the message", []durable.Op{{"p", partial("content"), 0, 1, []any{text}}}, "message"},
		{"a non-splice on the content array sends the message", []durable.Op{{"s", partial("content"), []any{}}}, "message"},
		{"a non-content field sends the message", []durable.Op{{"s", partial("stopReason"), "stop"}}, "message"},
		{"the partial itself replaced sends the message", []durable.Op{{"s", partial(), map[string]any{}}}, "message"},
		{"an ancestor of the partial replaced sends the message", []durable.Op{{"s", []any{"docs", "pi.live"}, map[string]any{}}}, "message"},
		{"usage adds nothing", []durable.Op{{"s", partial("usage", "input"), 3}}, ""},
		{"a path outside the partial adds nothing", []durable.Op{{"a", []any{"docs", "pi.live", "tools", 0, "output"}, "x"}}, ""},
		{"a set on a block sends the block", []durable.Op{{"s", partial("content", 0, "text"), "all"}}, "block@0"},
		{"an append to a non-streamed field sends the block", []durable.Op{{"a", partial("content", 0, "textSignature"), "s"}}, "block@0"},
		{"an append below the text field sends the block", []durable.Op{{"a", partial("content", 0, "text", "x"), "s"}}, "block@0"},
		{"a block sent whole absorbs later changes to it in the batch", []durable.Op{
			{"s", partial("content", 0, "text"), "all"}, {"a", partial("content", 0, "text"), "+"}, {"a", partial("content", 1, "text"), "+"}, {"s", partial("content", 0, "text"), "again"},
		}, "block@0 text_delta@1[+]"},
	}
	message := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: "hi"}, ai.TextContent{Text: "x"}, ai.TextContent{Text: "y"}}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			changes := messageChanges(tc.ops, nil, message)
			var got string
			for i, change := range changes {
				if i > 0 {
					got += " "
				}
				switch change.Type {
				case "text_delta", "thinking_delta":
					got += fmt.Sprintf("%s@%d[%s]", change.Type, change.ContentIndex, change.Delta)
				case "toolcall_delta":
					path := ""
					for j, segment := range change.Path {
						if j > 0 {
							path += "/"
						}
						path += fmt.Sprint(segment)
					}
					got += fmt.Sprintf("%s@%d[%s:%s]", change.Type, change.ContentIndex, path, change.Delta)
				case "message":
					if change.Message != message {
						t.Fatalf("message change carries %v, want the current message", change.Message)
					}
					got += "message"
				case "block":
					if change.Block != message.Content[change.ContentIndex] {
						t.Fatalf("block change at %d carries %v, want the current content block", change.ContentIndex, change.Block)
					}
					got += fmt.Sprintf("block@%d", change.ContentIndex)
				default:
					got += fmt.Sprintf("%s@%d", change.Type, change.ContentIndex)
				}
			}
			if got != tc.want {
				t.Errorf("changes = %q, want %q", got, tc.want)
			}
		})
	}
}
