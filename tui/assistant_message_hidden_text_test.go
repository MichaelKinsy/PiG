package tui

import (
	"strings"
	"testing"
)

// hiddenRowWant is the hidden-thinking label as assistant-message.ts draws it: theme.italic(theme.fg("thinkingText", label)),
// written out here from the escape sequences rather than through the helpers the component uses.
func hiddenRowWant(color, label string, modifiers bool) string {
	text := color + label + FgClose(color)
	if modifiers {
		return "\x1b[3m" + text + "\x1b[23m"
	}
	return text
}

// hiddenThinkingRows renders block and returns the rows that show label.
func hiddenThinkingRows(block *AssistantMessageComponent, label string) []string {
	var rows []string
	for _, row := range block.Render(80) {
		if strings.Contains(row, label) {
			rows = append(rows, row)
		}
	}
	return rows
}

// The hidden-thinking row is the same bytes whether the block built it just now or kept it from an earlier click, and it takes
// the new styling at once after each input of it changes: the theme's thinking colour, chalk's colour level (modifiers on or off)
// and the label. A kept row keyed on too few of these shows the old styling.
func TestHiddenThinkingRowFollowsItsInputs(t *testing.T) {
	previous := ActiveTheme()
	t.Cleanup(func() { SetThemeInstance(previous); SetChalkModifiers(true) })
	second, err := builtinDarkTheme().WithTokenColors(map[string]Color{"thinkingText": mustParseColor(t, "#123456")})
	if err != nil {
		t.Fatal(err)
	}
	if second.ThinkingText == builtinDarkTheme().ThinkingText {
		t.Fatal("the second theme has the same thinking colour as the first")
	}
	SetThemeInstance(builtinDarkTheme())
	SetChalkModifiers(true)

	label := "Thinking hidden"
	block := NewAssistantMessageComponent(nil, false, nil, "", nil, nil, "", "")
	block.SetHiddenThinkingLabel(label)
	block.SetContent([]AssistantSegment{{Thinking: true, Text: "reasoning"}, {Text: "answer"}})
	block.Render(80)
	click := func() { block.thinkingRuns[0].region.onMouse(TuiMouseEvent{Type: MouseClick, Button: MouseButtonLeft}) }

	steps := []struct {
		name  string
		apply func()
	}{
		{"default theme, modifiers on", func() {}},
		{"second theme", func() { SetThemeInstance(second) }},
		{"modifiers off", func() { SetChalkModifiers(false) }},
		{"modifiers on again", func() { SetChalkModifiers(true) }},
		{"changed label", func() { label = "Reasoning is folded"; block.SetHiddenThinkingLabel(label) }},
		{"first theme again", func() { SetThemeInstance(builtinDarkTheme()) }},
		{"modifiers off with the second theme", func() { SetThemeInstance(second); SetChalkModifiers(false) }},
	}
	click() // the run starts hidden
	for _, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			step.apply()
			block.Invalidate()
			want := hiddenRowWant(ActiveTheme().ThinkingText, label, !chalkModifiersOff.Load())
			fresh := NewAssistantMessageComponent(nil, false, nil, "", nil, nil, "", "")
			fresh.SetHiddenThinkingLabel(label)
			fresh.SetContent([]AssistantSegment{{Thinking: true, Text: "reasoning"}, {Text: "answer"}})
			fresh.thinkingRuns[0].region.onMouse(TuiMouseEvent{Type: MouseClick, Button: MouseButtonLeft})
			built := hiddenThinkingRows(fresh, label)
			// The block's hidden row right after the change, then after a click that shows the run and one that hides it again.
			for _, when := range []string{"after the change", "after a show click and a hide click"} {
				got := hiddenThinkingRows(block, label)
				if len(got) != 1 || !strings.Contains(got[0], want) {
					t.Fatalf("hidden row %s = %q, want it to hold %q", when, got, want)
				}
				if len(built) != 1 || built[0] != got[0] {
					t.Fatalf("hidden row %s = %q, a block built now draws %q", when, got, built)
				}
				click()
				click()
			}
		})
	}
}
