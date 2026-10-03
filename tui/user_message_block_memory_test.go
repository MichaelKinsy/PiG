package tui

import "testing"

// user-message.ts:38-59 (Pi 1.0.0): the Markdown pads and colors its own background, so no Box keeps a second full-width copy of every line. A cached render therefore costs only the returned copy of the Markdown's cached lines and the two zone-marker lines: one slice and two strings.
func TestUserMessageBlockCachedRenderKeepsOneCopy(t *testing.T) {
	block := NewUserMessageBlock("hello **world**\n\nsecond paragraph with more text")
	first := block.Render(80)
	if len(first) < 2 {
		t.Fatalf("lines = %q", first)
	}
	const wantAllocs = 1 + 2 // slices.Clone of the cached lines, then the first and last lines with their OSC 133 markers
	if got := testing.AllocsPerRun(100, func() { block.Render(80) }); got > wantAllocs {
		t.Fatalf("cached Render allocates %v times, want at most %d", got, wantAllocs)
	}
}
