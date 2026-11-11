package tui

import "testing"

// Ports packages/tui/test/text.test.ts: "keeps its wrapped lines when the same text is set again".
func TestUpstreamTextKeepsItsWrappedLinesWhenTheSameTextIsSetAgain(t *testing.T) {
	text := NewPaddedText("hello world", 0, 0, nil)
	lines := text.Render(5)
	if len(lines) == 0 {
		t.Fatal("no lines rendered")
	}
	text.SetText("hello world")
	again := text.Render(5)
	if len(again) != len(lines) || &again[0] != &lines[0] {
		t.Fatalf("SetText with the same text wrapped it again: %q then %q", lines, again)
	}
	text.SetText("hello there")
	if changed := text.Render(5); &changed[0] == &lines[0] {
		t.Fatalf("SetText with new text kept the old lines %q", changed)
	}
}
