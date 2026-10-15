package ai

import (
	"encoding/json"
	"strings"
	"testing"
)

func BenchmarkAnthropicToolResultContent(b *testing.B) {
	text := TextContent{Text: strings.Repeat("tool output ", 1024)}
	image := ImageContent{Data: strings.Repeat("a", 4096), MimeType: "image/png"}
	for _, tc := range []struct {
		name    string
		content []ToolResultMessageContent
	}{
		{"text only", []ToolResultMessageContent{TextContent{}, text, text}},
		{"mixed", []ToolResultMessageContent{text, image, TextContent{}, text}},
		{"image only", []ToolResultMessageContent{image}},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				encoded, err := json.Marshal(anthToolResultContent(tc.content))
				if err != nil || len(encoded) == 0 {
					b.Fatalf("marshal result: %v", err)
				}
			}
		})
	}
}

// packages/ai/src/api/anthropic-messages.ts:128-176 preserves mixed block order, counts empty text as text, and joins every text-only element (including empty elements).
func TestAnthropicToolResultContentOrderAndEmptyText(t *testing.T) {
	image := ImageContent{Data: "eA==", MimeType: "image/png"}
	const imageJSON = `{"type":"image","source":{"type":"base64","media_type":"image/png","data":"eA=="}}`
	for _, tc := range []struct {
		name    string
		content []ToolResultMessageContent
		want    string
	}{
		{"empty result", nil, `""`},
		{"empty first text", []ToolResultMessageContent{TextContent{}, TextContent{Text: "after"}}, `"\nafter"`},
		{"two empty texts", []ToolResultMessageContent{TextContent{}, TextContent{}}, `"\n"`},
		{"interior empty text", []ToolResultMessageContent{TextContent{Text: "before"}, TextContent{}, TextContent{Text: "after"}}, `"before\n\nafter"`},
		{"image before text", []ToolResultMessageContent{image, TextContent{Text: "after"}}, `[` + imageJSON + `,{"type":"text","text":"after"}]`},
		{"text around image", []ToolResultMessageContent{TextContent{Text: "before"}, image, TextContent{Text: "after"}}, `[{"type":"text","text":"before"},` + imageJSON + `,{"type":"text","text":"after"}]`},
		{"image with empty text", []ToolResultMessageContent{image, TextContent{}}, `[` + imageJSON + `,{"type":"text","text":""}]`},
		{"whitespace text with image", []ToolResultMessageContent{TextContent{Text: " "}, image}, `[{"type":"text","text":" "},` + imageJSON + `]`},
		{"image only placeholder", []ToolResultMessageContent{image}, `[{"type":"text","text":"(see attached image)"},` + imageJSON + `]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Run("converter", func(t *testing.T) {
				encoded, err := json.Marshal(anthToolResultContent(tc.content))
				if err != nil {
					t.Fatal(err)
				}
				assertShapeJSON(t, encoded, tc.want)
			})
			t.Run("provider caller", func(t *testing.T) {
				model := emptySignatureModel(nil)
				model.Input = []string{"text", "image"}
				input := Context{Messages: []Message{
					UserMessage{Content: UserText("use tool")},
					AssistantMessage{API: APIAnthropicMessages, Provider: model.ProviderMeta.ProviderID, Model: model.ID, StopReason: StopReasonToolUse, Content: []AssistantContentBlock{ToolCall{ID: "tool_1", Name: "lookup", Arguments: JsonObject{}}}},
					ToolResultMessage{ToolCallID: "tool_1", ToolName: "lookup", Content: tc.content},
				}}
				payload := upstreamAnthropicParams(t, model, input, StreamOptions{CacheRetention: CacheRetentionNone})
				var messages []struct {
					Role    string          `json:"role"`
					Content json.RawMessage `json:"content"`
				}
				if err := json.Unmarshal(payload["messages"], &messages); err != nil {
					t.Fatal(err)
				}
				if len(messages) != len(input.Messages) {
					t.Fatalf("messages=%s", payload["messages"])
				}
				last := messages[len(messages)-1]
				if last.Role != "user" {
					t.Fatalf("tool result role=%s", last.Role)
				}
				var blocks []struct {
					Type    string          `json:"type"`
					Content json.RawMessage `json:"content"`
				}
				if err := json.Unmarshal(last.Content, &blocks); err != nil {
					t.Fatal(err)
				}
				if len(blocks) != 1 || blocks[0].Type != "tool_result" {
					t.Fatalf("tool result blocks=%s", last.Content)
				}
				assertShapeJSON(t, blocks[0].Content, tc.want)
			})
		})
	}
}
