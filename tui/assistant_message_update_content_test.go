package tui

// pi: packages/coding-agent/src/modes/interactive/components/assistant-message.ts

import (
	"strings"
	"testing"
)

func assistantPlain(b *AssistantMessageComponent) string {
	return stripANSI(strings.Join(b.Render(60), "\n"))
}

// upstream: assistant-message.ts:26-49,91-170 the constructor renders its message through updateContent; text blocks render in
// order, a thinking run is hidden behind the label when hideThinkingBlock is set, and a tool call is an invisible boundary
// that turns off the aborted/error diagnostic.
func TestAssistantMessageUpdateContentFollowsPi(t *testing.T) {
	message := AssistantMessage{Content: []AssistantContentBlock{
		{Type: "thinking", Thinking: "pondering"},
		{Type: "text", Text: "answer"},
	}}
	visible := NewAssistantMessageComponent(&message, false, nil, "", nil, nil)
	if got := assistantPlain(visible); !strings.Contains(got, "pondering") || !strings.Contains(got, "answer") {
		t.Fatalf("constructor message not rendered: %q", got)
	}
	hidden := NewAssistantMessageComponent(&message, true, nil, "", nil, nil)
	if got := assistantPlain(hidden); strings.Contains(got, "pondering") || !strings.Contains(got, "Thinking...") || !strings.Contains(got, "answer") {
		t.Fatalf("hidden thinking: %q", got)
	}

	aborted := AssistantMessage{StopReason: "aborted", ErrorMessage: "Request was aborted", Content: []AssistantContentBlock{{Type: "text", Text: "partial"}}}
	b := NewAssistantMessageComponent(nil, false, nil, "", nil, nil)
	b.UpdateContent(aborted)
	if got := assistantPlain(b); !strings.Contains(got, "Operation aborted") {
		t.Fatalf("aborted diagnostic missing: %q", got)
	}
	aborted.Content = append(aborted.Content, AssistantContentBlock{Type: "toolCall"})
	b.UpdateContent(aborted)
	if got := assistantPlain(b); strings.Contains(got, "Operation aborted") {
		t.Fatalf("a tool call must suppress the aborted diagnostic: %q", got)
	}
	if lines := b.Render(60); strings.Contains(strings.Join(lines, ""), assistantZoneStart) {
		t.Fatal("a message with tool calls must carry no OSC 133 zones")
	}
	b.UpdateContent(AssistantMessage{StopReason: "length", Content: []AssistantContentBlock{{Type: "text", Text: "cut"}, {Type: "toolCall"}}})
	if got := assistantPlain(b); !strings.Contains(got, "Response was truncated before completion.") {
		t.Fatalf("length diagnostic missing: %q", got)
	}
	b.UpdateContent(AssistantMessage{StopReason: "error", ErrorMessage: "boom"})
	if got := assistantPlain(b); !strings.Contains(got, "Error: boom") {
		t.Fatalf("error diagnostic missing: %q", got)
	}
}

// upstream: assistant-message.ts:91 `isStreaming = this.isStreaming`: an omitted argument keeps the previous flag.
func TestAssistantMessageUpdateContentKeepsStreamingFlag(t *testing.T) {
	b := NewAssistantMessageComponent(nil, false, nil, "", nil, nil)
	if b.IsStreaming() {
		t.Fatal("a new block is not streaming")
	}
	b.UpdateContent(AssistantMessage{}, true)
	b.UpdateContent(AssistantMessage{})
	if !b.IsStreaming() {
		t.Fatal("an omitted isStreaming must keep the retained flag")
	}
	b.UpdateContent(AssistantMessage{}, false)
	if b.IsStreaming() {
		t.Fatal("isStreaming=false must replace the flag")
	}
}

// upstream: assistant-message.ts:26-40,111-114 the constructor's markdownTransformers rewrite text ("assistant") and visible
// thinking ("assistant-thinking") with the block's streaming flag, markdown-transform.ts:12-29 applyMarkdownTransformers: each
// transformer sees the previous output, and a thrown error or a non-string result keeps the current markdown.
func TestAssistantMessageConstructorMarkdownTransformers(t *testing.T) {
	var contexts []MarkdownTransformContext
	record := func(markdown string, context MarkdownTransformContext) (string, bool) {
		contexts = append(contexts, context)
		return markdown + "!", true
	}
	boom := func(string, MarkdownTransformContext) (string, bool) { panic("boom") }
	undefined := func(string, MarkdownTransformContext) (string, bool) { return "ignored", false }
	upper := func(markdown string, _ MarkdownTransformContext) (string, bool) {
		return strings.ToUpper(markdown), true
	}
	message := AssistantMessage{Content: []AssistantContentBlock{{Type: "thinking", Thinking: "hmm"}, {Type: "text", Text: "hello"}}}
	b := NewAssistantMessageComponent(nil, false, nil, "", nil, []MarkdownTransformer{record, boom, undefined, upper})
	b.UpdateContent(message, true)
	got := assistantPlain(b)
	if !strings.Contains(got, "HELLO!") || !strings.Contains(got, "HMM!") {
		t.Fatalf("transformers did not rewrite text and thinking in order: %q", got)
	}
	types := map[string]bool{}
	for _, c := range contexts {
		types[c.MessageType] = true
		if !c.IsStreaming || c.AvailableWidth <= 0 {
			t.Fatalf("context = %+v, want streaming with the render width", c)
		}
	}
	if !types["assistant"] || !types["assistant-thinking"] {
		t.Fatalf("message types seen: %v", types)
	}
	contexts = nil
	b.UpdateContent(message, false)
	assistantPlain(b)
	for _, c := range contexts {
		if c.IsStreaming {
			t.Fatalf("a finished block must report isStreaming=false: %+v", c)
		}
	}
	if len(contexts) == 0 {
		t.Fatal("UpdateContent did not re-run the transformers")
	}
}
