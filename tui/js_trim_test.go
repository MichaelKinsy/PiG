package tui

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// String.prototype.trim (text.ts:57) treats U+FEFF as whitespace and U+0085 as an ordinary character, so a Text of only U+FEFF renders nothing and one of U+0085 renders a line.
func TestTextBlankCheckTrimsLikeJavaScript(t *testing.T) {
	for content, blank := range map[string]bool{"\ufeff": true, " " + "\ufeff" + " ": true, "\u00a0": true, "\u0085": false, "x": false} {
		if got := len(NewText(content).Render(20)) == 0; got != blank {
			t.Errorf("%q renders blank = %v, want %v", content, got, blank)
		}
	}
}

// assistant-message.ts:110-125 trims a text or thinking run with String.prototype.trim before it renders: a run of only U+FEFF is blank and a leading U+FEFF is dropped, while U+0085 is kept.
func TestAssistantMessageComponentTrimsRunsLikeJavaScript(t *testing.T) {
	render := func(segments ...AssistantSegment) string {
		block := NewAssistantMessageComponent(nil, false, nil, "", nil, nil)
		block.SetContent(segments)
		return strings.Join(block.Render(40), "\n")
	}
	for _, thinking := range []bool{false, true} {
		if got := render(AssistantSegment{Thinking: thinking, Text: "\ufeff \ufeff"}); strings.TrimSpace(widthx.StripAnsi(got)) != "" {
			t.Errorf("thinking=%v: a run of only U+FEFF rendered %q", thinking, got)
		}
		if got := render(AssistantSegment{Thinking: thinking, Text: "\ufeffhello"}); strings.Contains(got, "\ufeff") || !strings.Contains(got, "hello") {
			t.Errorf("thinking=%v: a leading U+FEFF survived or the text is gone: %q", thinking, got)
		}
		if got := render(AssistantSegment{Thinking: thinking, Text: "\u0085hello"}); !strings.Contains(got, "\u0085") {
			t.Errorf("thinking=%v: U+0085 is not whitespace to String.prototype.trim and must be kept: %q", thinking, got)
		}
	}
}
