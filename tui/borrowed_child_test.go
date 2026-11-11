package tui

import (
	"slices"
	"strings"
	"testing"
)

// A container that borrows a streaming assistant message's rows (the layout frame, a parent container) gets the same rows as
// a caller of Render, whatever mix of the two renders the message, and the arrays Render hands out stay as they were.
func TestBorrowedChildRenderEqualsRender(t *testing.T) {
	deltas := []string{
		"## Title\n\nA paragraph with `code` and ", "**bold** text that wraps because it is long enough to need more than one row at a narrow width.\n\n",
		"- one\n- two\n\n```go\nfunc main() {\n", "\tprintln(1)\n}\n```\n\n", "| a | b |\n|---|---|\n| 1 | 2 |\n\n", "tail text",
	}
	for _, pad := range []int{0, 2} {
		chat := NewContainer()
		borrowed := NewAssistantMessageComponent(nil, false, nil, "", &pad, nil, "", "")
		reference := NewAssistantMessageComponent(nil, false, nil, "", &pad, nil, "", "")
		chat.Add(borrowed)
		chat.Add(NewSpacer(1))
		var kept [][]string
		var want [][]string
		for step, delta := range deltas {
			for _, width := range []int{60, 24, 60} {
				borrowed.SetTextDelta(delta)
				reference.SetTextDelta(delta)
				if step%2 == 1 {
					borrowed.Render(width) // the public render between two borrowed ones
				}
				got := chat.Render(width)
				expected := append(reference.Render(width), "")
				if !slices.Equal(got, expected) {
					t.Fatalf("pad %d step %d width %d: container rows differ from the block's own render\n got %q\nwant %q", pad, step, width, got, expected)
				}
				if again := chat.renderBorrowed(width); !slices.Equal(again, expected) {
					t.Fatalf("pad %d step %d width %d: borrowed rows differ\n got %q\nwant %q", pad, step, width, again, expected)
				}
				kept, want = append(kept, got), append(want, slices.Clone(got))
				own := borrowed.Render(width)
				kept, want = append(kept, own), append(want, slices.Clone(own))
				text := borrowed.md.Render(width)
				kept, want = append(kept, text), append(want, slices.Clone(text))
			}
		}
		for i := range kept {
			if !slices.Equal(kept[i], want[i]) {
				t.Fatalf("pad %d: rows returned by render %d changed after a later render:\n got %q\nwant %q", pad, i, kept[i], want[i])
			}
		}
		if !strings.Contains(strings.Join(chat.Render(60), "\n"), "tail text") {
			t.Fatal("the streamed text is missing")
		}
	}
}
