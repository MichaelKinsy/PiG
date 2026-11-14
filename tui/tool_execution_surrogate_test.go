package tui

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// pi: packages/coding-agent/src/modes/interactive/components/tool-execution.ts (a card without a renderer shows the tool's name),
// packages/tui/src/terminal.ts (a frame is written to stdout as UTF-8, which turns a lone UTF-16 surrogate into U+FFFD)

// A tool whose name holds a lone UTF-16 surrogate, held in a Go string in its three-byte form, draws its card: the width functions take the
// text at every width, no line is wider than the card, and the frame as the terminal receives it shows one U+FFFD for the unit.
func TestToolCardDrawsANameWithALoneSurrogate(t *testing.T) {
	for _, width := range []int{80, 24, 9} {
		card := NewToolExecutionComponent("N[\xed\xb8\x80]", "call-1", json.RawMessage(`{}`), ToolExecutionOptions{}, nil, nil, "")
		lines := card.Render(width)
		for _, line := range lines {
			if got := widthx.VisibleWidth(line); got > width {
				t.Errorf("width %d: a line is %d cells wide: %q", width, got, line)
			}
		}
		if width != 80 {
			continue
		}
		if frame := jsstring.StreamText(strings.Join(lines, "\n")); !strings.Contains(frame, "N[\uFFFD]") || strings.Contains(frame, "\xed") {
			t.Errorf("the frame the terminal receives does not show the unit as one U+FFFD: %q", frame)
		}
	}
}
