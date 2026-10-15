package harness

import (
	"encoding/json"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// upstream: packages/durable/src/harness/events.ts:28-33 MessageChange is the union
// `{type: text_start|thinking_start|toolcall_start, contentIndex, block}` | `{type: text_delta|thinking_delta, contentIndex, delta}` |
// `{type: toolcall_delta, contentIndex, path, delta}` | `{type: block, contentIndex, block}` | `{type: message, message}`, carried by
// `message_update.changes` (:64). The Go struct holds every member, so its JSON form must carry exactly the members of its variant, in declaration order.
// mutation-checked: a toolcall_delta without its path, a delta variant that carries a block, the message variant carrying a contentIndex, and a start
// variant without its block each fail it.
func TestMessageChangeWireFormMatchesEachUpstreamVariantUpstream(t *testing.T) {
	text := ai.TextContent{Text: "hi"}
	message := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{text}}
	for _, test := range []struct {
		name   string
		change MessageChange
		want   string
	}{
		{"text_start", MessageChange{Type: "text_start", ContentIndex: 1, Block: text}, `{"type":"text_start","contentIndex":1,"block":{"type":"text","text":"hi"}}`},
		{"thinking_start", MessageChange{Type: "thinking_start", ContentIndex: 0, Block: ai.ThinkingContent{Thinking: "t"}}, `{"type":"thinking_start","contentIndex":0,"block":{"type":"thinking","thinking":"t"}}`},
		{"text_delta", MessageChange{Type: "text_delta", ContentIndex: 2, Delta: "lo"}, `{"type":"text_delta","contentIndex":2,"delta":"lo"}`},
		{"thinking_delta", MessageChange{Type: "thinking_delta", ContentIndex: 0, Delta: "mm"}, `{"type":"thinking_delta","contentIndex":0,"delta":"mm"}`},
		{"toolcall_delta", MessageChange{Type: "toolcall_delta", ContentIndex: 1, Path: []any{"a", 0}, Delta: `"v`}, `{"type":"toolcall_delta","contentIndex":1,"path":["a",0],"delta":"\"v"}`},
		{"toolcall_delta at the root", MessageChange{Type: "toolcall_delta", ContentIndex: 1, Path: []any{}, Delta: "x"}, `{"type":"toolcall_delta","contentIndex":1,"path":[],"delta":"x"}`},
		{"block", MessageChange{Type: "block", ContentIndex: 3, Block: text}, `{"type":"block","contentIndex":3,"block":{"type":"text","text":"hi"}}`},
		{"message", MessageChange{Type: "message", ContentIndex: 9, Message: message}, `{"type":"message","message":` + mustMarshal(t, message) + `}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := json.Marshal(test.change)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != test.want {
				t.Errorf("wire form = %s, want %s", got, test.want)
			}
		})
	}
}

func mustMarshal(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
