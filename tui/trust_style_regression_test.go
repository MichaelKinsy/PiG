package tui

import (
	"strings"
	"testing"
)

func TestBorderAndKeyHintPreserveEnclosingStyles(t *testing.T) {
	// Pi dynamic-border.ts and keybinding-hints.ts use theme.fg, whose close sequence resets foreground only, not an enclosing bold/background style.
	t.Run("border", func(t *testing.T) {
		for _, width := range []int{0, 1, 120} {
			got := NewDynamicBorder("\x1b[31m").Render(width)
			want := "\x1b[31m" + strings.Repeat("─", max(1, width)) + "\x1b[39m"
			if len(got) != 1 || got[0] != want {
				t.Fatalf("width %d: %q, want %q", width, got, want)
			}
		}
	})
	t.Run("hint", func(t *testing.T) {
		theme := ActiveTheme()
		want := theme.FgText("dim", "enter") + theme.FgText("muted", " save")
		if got := RawKeyHint("enter", "save"); got != want {
			t.Fatalf("hint=%q, want %q", got, want)
		}
	})
}
