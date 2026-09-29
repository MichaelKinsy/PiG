package tui

import (
	"fmt"
	"strings"
	"testing"
)

// Pi packages/tui/src/components/editor.ts:276-295,508-516 lays out/truncates the uncolored border, then calls borderColor once for the complete row.
func TestEditorScrollBorderCallbackLayout(t *testing.T) {
	for _, tc := range []struct {
		width       int
		top, bottom string
	}{
		{40, strings.Repeat("─", 15) + " ↑ 9 more " + strings.Repeat("─", 15), strings.Repeat("─", 15) + " ↓ 4 more " + strings.Repeat("─", 15)},
		{10, "─── ↑ 9...", "─── ↓ 4..."},
	} {
		t.Run(fmt.Sprint(tc.width), func(t *testing.T) {
			e := NewEditor()
			e.SetMaxVisibleLines(7)
			var colored []string
			color := func(text string) string { colored = append(colored, text); return "\x1b[35m" + text + "\x1b[39m" }
			e.BorderColor = color
			lines := make([]string, 20)
			for i := range lines {
				lines[i] = fmt.Sprintf("line %d", i)
			}
			e.SetText(strings.Join(lines, "\n"))
			e.Render(tc.width)
			for range 10 {
				e.HandleInput("\x1b[A")
			}
			colored = nil
			rows := e.Render(tc.width)
			if rows[0] != "\x1b[35m"+tc.top+"\x1b[39m" || rows[len(rows)-1] != "\x1b[35m"+tc.bottom+"\x1b[39m" {
				t.Fatalf("borders=%q / %q, want %q / %q", rows[0], rows[len(rows)-1], tc.top, tc.bottom)
			}
			if len(colored) != 2 || colored[0] != tc.top || colored[1] != tc.bottom {
				t.Fatalf("callback inputs=%q", colored)
			}
		})
	}
}
