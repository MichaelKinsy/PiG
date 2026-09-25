package tui

import (
	"slices"
	"strings"
	"testing"
)

func TestAssistantMessageBlock_DeltasUseOrderedTrimmedContent(t *testing.T) {
	b := NewAssistantMessageBlock(false)
	b.SetTextDelta("  before \n")
	b.SetThinkingDelta("  think ")
	b.SetThinkingDelta("again  ")
	b.SetTextDelta(" after  ")
	want := NewAssistantMessageBlock(false)
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
			b := NewAssistantMessageBlock(false)
			b.SetContent([]AssistantSegment{{Text: " \n "}, {Thinking: true, Text: " "}})
			b.SetTerminalError(tc.stop, tc.err)
			want := []string{"", " " + ActiveTheme().Error + tc.want + SGRFgReset}
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
