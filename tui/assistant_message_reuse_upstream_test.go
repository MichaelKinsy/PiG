package tui

import (
	"slices"
	"testing"
)

// Ports the first test of packages/coding-agent/test/assistant-message.test.ts: "reuses its Markdown across streaming
// and its end, and renders like a new component after a theme change". The test's spy on syntax-highlight.ts highlight
// is a counting highlighter in the Markdown theme.
func TestUpstreamAssistantMessageReusesItsMarkdownAcrossStreamingAndItsEnd(t *testing.T) {
	original := ActiveTheme().Name
	t.Cleanup(func() { SetTheme(original) })
	SetTheme("dark")
	var calls []string
	var streaming []bool
	code := "```ts\nconst a = 1;\n```"
	content := func(text string) AssistantMessage {
		return AssistantMessage{Content: []AssistantContentBlock{
			{Type: "thinking", Thinking: "Thinking with **bold** text"},
			{Type: "text", Text: text},
		}}
	}
	one := 1
	component := NewAssistantMessageComponent(nil, false, countingMarkdownTheme(&calls), "Thinking...", &one, []MarkdownTransformer{
		func(markdown string, context MarkdownTransformContext) (string, bool) {
			streaming = append(streaming, context.IsStreaming)
			return markdown, true
		},
	}, "", "")
	component.UpdateContent(content(code), true)
	component.Render(80)
	message := content(code + "\n\nThe end.")
	component.UpdateContent(message, false)
	component.Render(80)
	if want := []string{"ts:const a = 1;"}; !slices.Equal(calls, want) {
		t.Fatalf("highlighter calls = %q, want %q", calls, want)
	}
	if len(streaming) == 0 || streaming[len(streaming)-1] {
		t.Fatalf("the last transform saw isStreaming = %v, want false", streaming)
	}

	// A theme change renders the same bytes as a component built after it.
	themed := NewAssistantMessageComponent(nil, false, nil, "Thinking...", &one, nil, "", "")
	themed.UpdateContent(content(code), true)
	themed.Render(80)
	themed.UpdateContent(message, false)
	themed.Render(80)
	SetTheme("light")
	themed.Invalidate()
	fresh := NewAssistantMessageComponent(&message, false, nil, "", nil, nil, "", "")
	if got, want := themed.Render(80), fresh.Render(80); !slices.Equal(got, want) {
		t.Fatalf("after a theme change\n got %q\nwant %q", got, want)
	}
}
