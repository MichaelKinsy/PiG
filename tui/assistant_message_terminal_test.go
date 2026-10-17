package tui

import (
	"slices"
	"strings"
	"testing"
)

func TestAssistantMessageBlock_DeltasUseOrderedTrimmedContent(t *testing.T) {
	b := NewAssistantMessageComponent(nil, false, nil, "", nil, nil)
	b.SetTextDelta("  before \n")
	b.SetThinkingDelta("  think ")
	b.SetThinkingDelta("again  ")
	b.SetTextDelta(" after  ")
	want := NewAssistantMessageComponent(nil, false, nil, "", nil, nil)
	want.SetContent([]AssistantSegment{{Text: "before"}, {Thinking: true, Text: "think again"}, {Text: "after"}})
	if got := b.Render(80); !slices.Equal(got, want.Render(80)) {
		t.Fatalf("delta render = %q, want %q", got, want.Render(80))
	}
	b.SetContent(nil)
	b.SetTextDelta("   ")
	if got := b.Render(80); len(got) != 0 {
		t.Fatalf("blank replacement must render nothing: %q", got)
	}
}

// Pi's terminal section always owns one spacer, uses an unprefixed length
// diagnostic, and treats only the exact default abort message as a placeholder.
func TestAssistantMessageBlock_TerminalContent(t *testing.T) {
	for _, tc := range []struct {
		name, stop, err, want string
	}{
		{"length", "length", "ignored", "Response was truncated before completion."},
		{"error", "error", "", "Error: Unknown error"},
		{"abort", "aborted", "Request was aborted", "Operation aborted"},
		{"custom-abort", "aborted", "provider context canceled after timeout", "provider context canceled after timeout"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := NewAssistantMessageComponent(nil, false, nil, "", nil, nil)
			b.SetContent([]AssistantSegment{{Text: " \n "}, {Thinking: true, Text: " "}})
			b.SetTerminalError(tc.stop, tc.err)
			want := []string{"\x1b]133;A\x07", "\x1b]133;B\x07\x1b]133;C\x07 " + ActiveTheme().Error + tc.want + SGRFgReset}
			want[1] += strings.Repeat(" ", 100-lineDisplayWidth(want[1]))
			if got := b.Render(100); !slices.Equal(got, want) {
				t.Fatalf("terminal output = %q, want %q", got, want)
			}
			b.SetContent([]AssistantSegment{{Text: "partial"}})
			got := b.Render(100)
			if len(got) != 4 || got[2] != "" || got[3] != want[1] {
				t.Fatalf("partial terminal output = %q", got)
			}
			b.SetTerminalError("stop", "")
			if strings.Contains(strings.Join(b.Render(100), "\n"), tc.want) {
				t.Fatal("terminal state survived replacement")
			}
		})
	}
}

// assistant-message.ts:13-100: AssistantMessageComponent extends Container with one content container; updateContent builds the
// leading spacer, one child per text block, a MouseRegion per thinking run and a spacer plus error Text after the content.
func TestAssistantMessageComponentIsAContainerOfAContentContainer(t *testing.T) {
	block := NewAssistantMessageComponent(nil, false, nil, "", nil, nil)
	if got := len(block.Children()); got != 1 {
		t.Fatalf("children = %d, want the content container only", got)
	}
	content := block.Children()[0].(*Container)
	if got := len(content.Children()); got != 0 {
		t.Fatalf("empty content container has %d children", got)
	}
	block.SetContent([]AssistantSegment{{Thinking: true, Text: "why"}, {Text: "answer"}})
	block.SetTerminalError("error", "boom")
	kinds := ""
	for _, child := range content.Children() {
		switch child.(type) {
		case *Spacer:
			kinds += "s"
		case *MouseRegion:
			kinds += "m"
		case *Markdown:
			kinds += "t"
		case *Text:
			kinds += "e"
		}
	}
	// spacer, thinking region, spacer (visible content follows), answer, spacer, error text.
	if kinds != "smstse" {
		t.Fatalf("content children = %q", kinds)
	}
}
