package tui

import (
	"strings"
	"testing"
)

func TestMultiTurnSpacing(t *testing.T) {
	c := NewContainer()
	// First user msg (no spacer)
	c.Add(NewUserMessageBlock("first message"))
	// Assistant reply
	amb := NewAssistantMessageBlock(false)
	amb.SetTextDelta("Hello")
	c.Add(amb)
	// Spacer before second user msg (mirrors upstream Spacer(1))
	c.Add(NewSpacer(1))
	// Second user msg
	c.Add(NewUserMessageBlock("second message"))
	// Second assistant reply
	amb2 := NewAssistantMessageBlock(false)
	amb2.SetTextDelta("World")
	c.Add(amb2)

	lines := c.Render(80)

	// Check that there are TWO blank-looking lines between "Hello" and "second message":
	// 1. Spacer(1) blank line
	// 2. UserMessageBlock top-pad (bg-painted, normalizes to blank)
	helloIdx := -1
	secondIdx := -1
	for i, l := range lines {
		s := strings.TrimRight(stripANSI(l), " ")
		if s == " Hello" {
			helloIdx = i
		}
		if s == " second message" {
			secondIdx = i
		}
	}
	if helloIdx == -1 {
		t.Fatal("Could not find ' Hello' line")
	}
	if secondIdx == -1 {
		t.Fatal("Could not find ' second message' line")
	}
	gap := secondIdx - helloIdx - 1
	t.Logf("Gap between 'Hello' (line %d) and 'second message' (line %d): %d blank lines", helloIdx, secondIdx, gap)
	if gap != 2 {
		t.Errorf("Expected 2 blank lines between assistant reply and next user message, got %d", gap)
		for i := helloIdx; i <= secondIdx; i++ {
			t.Logf("  line %d: stripped=%q", i, stripANSI(lines[i]))
		}
	}
}
