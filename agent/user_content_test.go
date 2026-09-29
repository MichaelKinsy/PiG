package agent

import (
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi session-manager.ts:439-451 only replaces null/missing user content; core/messages.ts:185 passes valid user messages through.
func TestUserContentUnionPreservesWireAndProviderShape(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		want      ai.UserContent
	}{
		{"empty string", `""`, ai.UserText("")},
		{"ordinary string", `"hello"`, ai.UserText("hello")},
		{"whitespace string", `" \n\t "`, ai.UserText(" \n\t ")},
		{"unicode string", `"é😀中"`, ai.UserText("é😀中")},
		{"empty array", `[]`, ai.UserContentBlocks{}},
		{"blocks", `[{"type":"text","text":"hello"},{"type":"image","data":"AQ==","mimeType":"image/png"}]`, ai.UserContentBlocks{ai.TextContent{Text: "hello"}, ai.ImageContent{Data: "AQ==", MimeType: "image/png"}}},
		{"large string", `"` + strings.Repeat("abcdef", 10000) + `"`, ai.UserText(strings.Repeat("abcdef", 10000))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wire := `{"role":"user","content":` + tc.raw + `,"timestamp":123}`
			var message AgentMessage
			if err := json.Unmarshal([]byte(wire), &message); err != nil {
				t.Fatal(err)
			}
			if message.User == nil || !reflect.DeepEqual(message.User.Content, tc.want) {
				t.Fatalf("decoded content=%#v want %#v", message.User, tc.want)
			}
			clone := message.Clone()
			if !reflect.DeepEqual(clone.User.Content, tc.want) {
				t.Fatalf("cloned content=%#v", clone.User.Content)
			}
			encoded, err := json.Marshal(clone)
			if err != nil {
				t.Fatal(err)
			}
			if string(encoded) != wire {
				t.Fatalf("wire changed: %s want %s", encoded, wire)
			}
			converted := ConvertToLLM([]AgentMessage{clone}, nil)
			if len(converted) != 1 {
				t.Fatalf("provider messages=%#v", converted)
			}
			user, ok := converted[0].(ai.UserMessage)
			if !ok || !reflect.DeepEqual(user.Content, tc.want) {
				t.Fatalf("provider message=%#v want content %#v", converted[0], tc.want)
			}
			if blocks, ok := clone.User.Content.(ai.UserContentBlocks); ok && len(blocks) > 0 {
				blocks[0] = ai.TextContent{Text: "changed clone"}
				if !reflect.DeepEqual(message.User.Content, tc.want) {
					t.Fatal("cloned array shares mutable storage")
				}
			}
		})
	}
}

func TestOmittedGoUserContentRemainsAnEmptyProviderArray(t *testing.T) {
	message := AgentMessage{User: &UserMessage{Role: RoleUser}}
	converted := ai.NormalizeContext(ai.Context{Messages: ConvertToLLM([]AgentMessage{message}, nil)}).Messages()
	user := converted[0].(ai.UserMessage)
	if !reflect.DeepEqual(user.Content, ai.UserContentBlocks{}) {
		t.Fatalf("omitted content became %#v, want an empty array", user.Content)
	}
}

func TestCustomStringCarriersStillConvertToTextBlocks(t *testing.T) {
	// core/messages.ts customMessageToLlm wraps custom strings; this differs from ordinary user strings.
	for _, content := range []any{"hello", ai.UserText("hello"), json.RawMessage(`"hello"`)} {
		messages := ConvertToLLM([]AgentMessage{{Custom: map[string]any{"role": RoleCustom, "content": content}}}, nil)
		want := ai.UserContentBlocks{ai.TextContent{Text: "hello"}}
		if len(messages) != 1 || !reflect.DeepEqual(messages[0].(ai.UserMessage).Content, want) {
			t.Fatalf("custom content %T converted to %#v", content, messages)
		}
	}
}

func BenchmarkUserContentUnion(b *testing.B) {
	for _, size := range []int{0, 64, 65536} {
		text := strings.Repeat("x", size)
		raw, err := json.Marshal(map[string]any{"role": "user", "content": text, "timestamp": 123})
		if err != nil {
			b.Fatal(err)
		}
		b.Run(strconv.Itoa(size), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				var message AgentMessage
				if err := json.Unmarshal(raw, &message); err != nil {
					b.Fatal(err)
				}
				clone := message.Clone()
				if _, err := json.Marshal(clone); err != nil {
					b.Fatal(err)
				}
				if len(ConvertToLLM([]AgentMessage{clone}, nil)) != 1 {
					b.Fatal("lost user")
				}
			}
		})
	}
}
