package harness

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
)

// A partial commit builds the JSON object of the partial without encoding and decoding its text, thinking and tracked arguments. The object
// is the one durable.ToJsonObject returns, except that the arguments of tracked tool calls are empty.
func TestPartialObjectEqualsTheEncodedPartial(t *testing.T) {
	long := strings.Repeat("streamed text with \"quotes\" and \\ escapes\n\t<&>\u2028é😀", 50)
	call := func(arguments ai.JsonObject) ai.ToolCall {
		return ai.ToolCall{ID: "c1", Name: "write", Arguments: arguments, ThoughtSignature: "ts", Namespace: "ns", ArgumentsError: "e"}
	}
	cases := []struct {
		name    string
		content []ai.AssistantContentBlock
		tracked map[int][]ai.JSONChange
	}{
		{"no blocks", nil, nil},
		{"empty text", []ai.AssistantContentBlock{ai.TextContent{}}, nil},
		{"text and thinking", []ai.AssistantContentBlock{ai.TextContent{Text: long, TextSignature: "sig"}, ai.ThinkingContent{Thinking: long, ThinkingSignature: "tsig", Redacted: true}}, nil},
		{"empty thinking signature", []ai.AssistantContentBlock{ai.ThinkingContent{Thinking: "x"}}, nil},
		{"invalid UTF-8 text", []ai.AssistantContentBlock{ai.TextContent{Text: "a\xffb"}, ai.ThinkingContent{Thinking: "\xed\xa0\x80lone surrogate"}}, nil},
		{"untracked call keeps its arguments", []ai.AssistantContentBlock{ai.TextContent{Text: "t"}, call(ai.JsonObject{"path": "a.ts", "content": long, "list": []any{"x", 1.5, nil, true}})}, nil},
		{"tracked call has empty arguments", []ai.AssistantContentBlock{ai.TextContent{Text: "t"}, call(ai.JsonObject{"path": "a.ts", "content": long})}, map[int][]ai.JSONChange{1: nil}},
		{"tracked index that is no call", []ai.AssistantContentBlock{ai.TextContent{Text: "t"}}, map[int][]ai.JSONChange{0: nil, 3: nil}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			partial := &ai.AssistantMessage{Content: c.content, API: "faux", Provider: "faux", Model: "faux-1", ResponseID: "r", Usage: ai.Usage{Input: 1, Output: 2, TotalTokens: 3}, StopReason: ai.StopReasonToolUse, Timestamp: 7}
			expected := *partial
			expected.Content = append([]ai.AssistantContentBlock(nil), c.content...)
			for index := range c.tracked {
				if index < len(expected.Content) {
					if tool, ok := expected.Content[index].(ai.ToolCall); ok {
						tool.Arguments = ai.JsonObject{}
						expected.Content[index] = tool
					}
				}
			}
			want, err := durable.ToJsonObject(&expected)
			if err != nil {
				t.Fatal(err)
			}
			got, err := partialObject(partial, c.tracked)
			if err != nil {
				t.Fatal(err)
			}
			gotText, _ := json.Marshal(got)
			wantText, _ := json.Marshal(want)
			if !bytes.Equal(gotText, wantText) {
				t.Fatalf("partial object differs\n got %.300s\nwant %.300s", gotText, wantText)
			}
			// json.Marshal writes invalid UTF-8 as U+FFFD on both sides: the strings are compared as they are.
			wantContent, _ := want.Value("content").([]any)
			gotContent, _ := got.Value("content").([]any)
			if len(gotContent) != len(wantContent) {
				t.Fatalf("%d blocks, want %d", len(gotContent), len(wantContent))
			}
			for index := range wantContent {
				for _, field := range []string{"text", "thinking"} {
					wantValue, _ := wantContent[index].(*delta.JsonObject).Get(field)
					gotValue, _ := gotContent[index].(*delta.JsonObject).Get(field)
					if wantValue != gotValue {
						t.Fatalf("content[%d].%s = %q, want %q", index, field, gotValue, wantValue)
					}
				}
			}
		})
	}
}

// A commit appends the text a stream grew by where the committed string plus that text has the new string's length, and compares the whole
// strings otherwise, which costs their length. The operations are the same either way, and a string compare allocates nothing in Go, so the
// plan BindPartial hands the commit is what shows that the appends are planned.
func TestBindPartialPlansAnAppendOfTheTextAndThinkingAStreamGrew(t *testing.T) {
	partial := &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: "Hello"}, ai.ThinkingContent{Thinking: "Hmm"}}, API: "faux", Provider: "faux", Model: "faux-1", StopReason: ai.StopReasonPending}
	changes := NewPartialChanges()
	changes.Observe(ai.TextDeltaEvent{ContentIndex: 0, Delta: " world", Partial: partial})
	changes.Observe(ai.ThinkingDeltaEvent{ContentIndex: 1, Delta: "m", Partial: partial})
	partial.Content = []ai.AssistantContentBlock{ai.TextContent{Text: "Hello world"}, ai.ThinkingContent{Thinking: "Hmmm"}}
	_, taken, err := changes.BindPartial(changes.Cut(), partial)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []struct {
		index       int
		field, text string
	}{{0, "text", " world"}, {1, "thinking", "m"}} {
		plan := taken.plan.field("content").field(want.index).field(want.field)
		if plan == nil || plan.Kind != planAppend || plan.Text != want.text {
			t.Fatalf("plan of content[%d].%s = %+v, want an append of %q", want.index, want.field, plan, want.text)
		}
	}
}
