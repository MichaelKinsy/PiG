package tui

import (
	"slices"
	"testing"
)

// Pi 1.0.0 user-message.ts:38-59 pads the Markdown and colors its background in the Markdown itself instead of in a Box
// around it, with identical output. The reference is the 0.99.2 composition: Box(outputPad, 1, userMessageBg) around
// Markdown(0, 0). Below width 2*outputPad+1 the outputs differ in Pi too: the Markdown keeps both margins around its
// one-column content (markdown.ts:338-357).
func TestUserMessageBlockPadsInTheMarkdownWithTheBoxOutput(t *testing.T) {
	boxed := func(text string, pad, width int) []string {
		inner := NewMarkdownWithOptions(text, 0, 0, nil, nil, &MarkdownOptions{PreserveOrderedListMarkers: true, PreserveBackslashEscapes: true})
		inner.SetDefaultColor(ActiveTheme().UserMessageText)
		box := NewPaddedBox(pad, 1, func(text string) string { return UserMessageBgOpen() + text + BgClose() })
		box.AddChild(inner)
		return box.Render(width)
	}
	for _, text := range []string{
		"",
		"   ",
		"hello",
		"a long line of user text that wraps across the width of a narrow terminal more than once",
		"1. first\n3. third\n\n- bullet with `code`\n\n> quoted **bold**",
		"```go\nfunc main() {}\n```\n\n# Heading\n\\*escaped\\*",
	} {
		for _, pad := range []int{0, 1} {
			for _, width := range []int{3, 12, 40} {
				block := NewUserMessageComponent(text, nil, 1, nil)
				block.SetOutputPad(pad)
				got := block.Render(width)
				want := slices.Clone(boxed(text, pad, width))
				if len(want) > 0 {
					want[0] = userMessageZoneStart + want[0]
					want[len(want)-1] = userMessageZoneEnd + want[len(want)-1]
				}
				if !slices.Equal(got, want) {
					t.Errorf("text %q pad %d width %d:\n got %q\nwant %q", text, pad, width, got, want)
				}
			}
		}
	}
}
