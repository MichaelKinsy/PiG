package tui

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

func TestAssistantThinkingTransformIsSeparateAndTracksState(t *testing.T) {
	b := NewAssistantMessageBlock(false)
	state := "streaming"
	b.SetMarkdownTransform(func(text string, _ int) string { return "text:" + text })
	b.SetThinkingMarkdownTransform(func(text string, width int) string {
		return fmt.Sprintf("thinking:%s:%d:%s", text, width, state)
	})
	b.SetMarkdownTransformState(func() string { return state })
	b.SetContent([]AssistantSegment{{Thinking: true, Text: "reason"}, {Text: "answer"}})
	for _, phase := range []string{"streaming", "settled"} {
		state = phase
		got := strings.Join(b.Render(80), "\n")
		if !strings.Contains(got, "thinking:reason:78:"+phase) || !strings.Contains(got, "text:answer") {
			t.Fatalf("transform context/state = %q", got)
		}
	}
	if b.Text() != "answer" || b.Thinking() != "reason" {
		t.Fatal("transforms changed stored content")
	}
	b.SetHiddenThinking(true)
	b.SetThinkingMarkdownTransform(func(string, int) string {
		t.Fatal("hidden thinking must not invoke its Markdown transformer")
		return ""
	})
	b.Render(80)
}

// The pinned Pi Markdown renderer applies the default thinking style to text
// tokens, not to headings, code spans, code fences or list markers. This exact
// foreground/decorations oracle comes from the review's bold/code/list vector.
func TestAssistantThinkingMarkdownUsesTokenStyles(t *testing.T) {
	b := NewAssistantMessageBlock(false)
	b.SetContent([]AssistantSegment{{Thinking: true, Text: "**Resumed bold** and `code`\n\n- first\n- second"}})
	th := ActiveTheme()
	style := "\x1b[3m" + th.ThinkingText
	closeStyle := SGRFgReset + SGRItalicReset
	want := []string{
		"",
		" \x1b[1m" + style + "Resumed bold" + closeStyle + SGRBoldDimReset + style + style + " and " + closeStyle + th.MDCode + "code" + SGRFgReset,
		" ",
		" " + th.MDListBullet + "- " + SGRFgReset + style + "first" + closeStyle,
		" " + th.MDListBullet + "- " + SGRFgReset + style + "second" + closeStyle,
	}
	if got := b.Render(80); !slices.Equal(got, want) {
		t.Fatalf("thinking token styles = %q, want %q", got, want)
	}
}

func TestMarkdownDefaultColorAppliesToListTextNotCode(t *testing.T) {
	th := ActiveTheme()
	m := NewMarkdown("- first `code` tail")
	m.SetDefaultColor(th.ThinkingText)
	want := []string{th.MDListBullet + "- " + SGRFgReset + th.ThinkingText + "first " + SGRFgReset + th.MDCode + "code" + SGRFgReset + th.ThinkingText + th.ThinkingText + " tail" + SGRFgReset}
	if got := m.Render(80); !slices.Equal(got, want) {
		t.Fatalf("default color list = %q, want %q", got, want)
	}
}
