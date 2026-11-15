//go:build !race

package tui

import "testing"

// A click on a thinking run that hides it, and the click that shows it again, rebuild the run's mouse region and the block's
// children, but not the hidden-thinking text: that is a function of the theme's thinking colour, chalk's colour level and the
// label, none of which a click changes, so the block keeps it. Building it again cost 6 allocations for the pair (20 against 14,
// measured by clearing the kept text before each click): the concatenation of colour, label and close sequence in thinkingRun
// and the strings.Builder growths in the ansiSpan that ChalkItalic draws with (tui/markdown.go: WriteString of open, text, close).
func TestThinkingRunClickKeepsTheHiddenText(t *testing.T) {
	block := NewAssistantMessageComponent(nil, false, nil, "", nil, nil, "", "")
	block.SetContent([]AssistantSegment{{Thinking: true, Text: "reasoning"}})
	block.Render(80)
	event := componentMouseEvent(MouseClick, 1, 1)
	DispatchMouseEvent(block, event) // hidden, the text is built
	pair := leastAllocs(100, func() {
		DispatchMouseEvent(block, event) // shown
		DispatchMouseEvent(block, event) // hidden
	})
	const ceiling = 14 // 20 before the text was kept
	t.Logf("allocations of a show click and a hide click: %.1f", pair)
	if pair > ceiling {
		t.Errorf("a show click and a hide click allocate %.1f times, ceiling %d", pair, ceiling)
	}
}
