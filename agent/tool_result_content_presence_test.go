package agent

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi 0.87.1 agent-loop.ts:863-867 explicitly constructs a text block even for
// an empty error message; createToolResultMessage at :880-894 retains the blocks.
func TestToolResultMessagePreservesEmptyErrorText(t *testing.T) {
	message := createToolResultMessage(finalizedToolCall{result: errorToolResult(""), isError: true}, 123)
	want := []ai.ToolResultMessageContent{ai.TextContent{Text: ""}}
	if !reflect.DeepEqual(message.Content, want) {
		t.Fatalf("content=%#v, want %#v", message.Content, want)
	}
}

func TestToolResultContentOverridePreservesPresence(t *testing.T) {
	for _, tc := range []struct {
		name     string
		override AfterToolCallResult
		want     []ai.ToolResultMessageContent
	}{
		{"keep", AfterToolCallResult{}, []ai.ToolResultMessageContent{ai.TextContent{Text: ""}}},
		{"explicit empty text", AfterToolCallResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: ""}}}, []ai.ToolResultMessageContent{ai.TextContent{Text: ""}}},
		{"clear text", AfterToolCallResult{Content: []ai.ToolResultMessageContent{}}, []ai.ToolResultMessageContent{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := NewAgent(AgentOptions{AfterToolCall: []AfterToolCallHook{func(context.Context, string, string, json.RawMessage, AgentToolResult) AfterToolCallResult {
				return tc.override
			}}})
			finalized := a.finalizeExecutedToolCall(t.Context(), preparedToolCall{}, finalizedToolCall{result: AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: ""}}}})
			message := createToolResultMessage(finalized, 123)
			if !reflect.DeepEqual(message.Content, tc.want) {
				t.Fatalf("content=%#v, want %#v", message.Content, tc.want)
			}
		})
	}
}

func BenchmarkToolResultMessageEmptyText(b *testing.B) {
	finalized := finalizedToolCall{result: AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: ""}}}}
	b.ReportAllocs()
	for b.Loop() {
		message := createToolResultMessage(finalized, 123)
		if _, err := json.Marshal(message); err != nil {
			b.Fatal(err)
		}
	}
}

// Pi's content:[] and image-only results must not gain a text block.
func TestToolResultMessageDoesNotInventText(t *testing.T) {
	image := ai.ImageContent{Data: "aW1n", MimeType: "image/png"}
	for _, tc := range []struct {
		name   string
		result AgentToolResult
		want   []ai.ToolResultMessageContent
	}{
		{"empty array", AgentToolResult{}, []ai.ToolResultMessageContent{}},
		{"image only", AgentToolResult{Content: []ai.ToolResultMessageContent{image}}, []ai.ToolResultMessageContent{image}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := createToolResultMessage(finalizedToolCall{result: tc.result}, 123)
			if !reflect.DeepEqual(got.Content, tc.want) {
				t.Fatalf("content=%#v, want %#v", got.Content, tc.want)
			}
		})
	}
}
