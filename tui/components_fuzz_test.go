package tui

import (
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

func checkFuzzWidth(t *testing.T, what string, lines []string, width int, input string) {
	t.Helper()
	for i, line := range lines {
		if w := widthx.VisibleWidth(line); w > width {
			t.Fatalf("%s line %d is %d columns wide, width is %d: %q\ninput %q", what, i, w, width, line, input)
		}
	}
}

// The text components and wrapping helpers never return a line wider than the width they were given, and never panic, whatever the text holds.
func FuzzTextComponentsStayWithinWidth(f *testing.F) {
	for _, seed := range []string{"hello world", "日本語のテキスト", "\x1b[31mred \x1b[1mbold\x1b[0m text", "tab\tseparated\tcolumns", "😀 👨‍👩‍👧 e\u0301", "a\x1b]8;;http://x\x07link\x1b]8;;\x07 b", "", "\n\n", "verylongwordwithoutanybreakopportunityatall", "\x1b[", "\x00\x01\x02"} {
		f.Add(seed, 12)
	}
	f.Fuzz(func(t *testing.T, text string, width int) {
		if width < 0 {
			width = -width
		}
		width = 2 + width%100 // one column cannot hold a double-width character in Pi either
		checkFuzzWidth(t, "NewText", NewText(text).Render(width), width, text)
		checkFuzzWidth(t, "TruncatedText", NewTruncatedText(text, 1, 1).Render(width), width, text)
		checkFuzzWidth(t, "WrapTextWithAnsi", widthx.WrapTextWithAnsi(text, width), width, text)
		if got := widthx.VisibleWidth(widthx.TruncateToWidth(text, width, "...", false)); got > width {
			t.Fatalf("TruncateToWidth is %d columns wide, width is %d\ninput %q", got, width, text)
		}
		input := NewInput(InputOptions{})
		input.Focused = true
		input.HandleInput(text)
		checkFuzzWidth(t, "Input", input.Render(width), width, text)
		// A Box pads both sides by PaddingX, so below two columns of padding plus one of content it overflows as Pi's Box does (contentWidth = max(1, width - 2 * paddingX)).
		if width >= 3 {
			box := NewBox()
			box.AddChild(NewText(text))
			checkFuzzWidth(t, "Box", box.Render(width), width, text)
		}
	})
}
