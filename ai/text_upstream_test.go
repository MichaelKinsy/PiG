package ai

import "testing"

func TestContentTextUpstream(t *testing.T) {
	content := []AssistantContentBlock{ThinkingContent{Thinking: "reasoning"}, TextContent{Text: "first"}, ToolCall{ID: "1", Name: "read", Arguments: JsonObject{}}, TextContent{Text: "second"}}
	// .upstream/v0.87.1/packages/ai/test/text.test.ts:12
	t.Run("extracts assistant text blocks", func(t *testing.T) {
		if got := ContentText(content); got != "first\nsecond" {
			t.Fatalf("contentText = %q", got)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/text.test.ts:16
	t.Run("supports custom separators", func(t *testing.T) {
		if got := ContentText(content, ""); got != "firstsecond" {
			t.Fatalf("contentText = %q", got)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/text.test.ts:20
	t.Run("passes string content through", func(t *testing.T) {
		if got := ContentText("hello"); got != "hello" {
			t.Fatalf("contentText = %q", got)
		}
	})
	// .upstream/v0.87.1/packages/ai/test/text.test.ts:24
	t.Run("extracts text from tool-result content", func(t *testing.T) {
		result := []ToolResultMessageContent{TextContent{Text: "first"}, ImageContent{Data: "...", MimeType: "image/png"}, TextContent{Text: "second"}}
		if got := ContentText(result, ""); got != "firstsecond" {
			t.Fatalf("contentText = %q", got)
		}
	})
}
