package tui

import (
	"fmt"
	"strings"
	"testing"
)

// upstream: packages/tui/src/components/editor.ts:1200-1217,2110-2129
func BenchmarkEditorEditUndo(b *testing.B) {
	for _, size := range []int{0, 32, 1024} {
		b.Run(fmt.Sprintf("chars-%d", size), func(b *testing.B) {
			text := strings.Repeat("a", size)
			edit := func() {
				e := NewEditor()
				e.SetMaxVisibleLines(7)
				for _, ch := range text {
					e.HandleInput(string(ch))
				}
				e.HandleInput("\x7f")
				e.HandleInput("\x1b[200~hello\nworld\x1b[201~")
				e.HandleInput(hostUndoKittyInput())
				e.Render(80)
			}
			// A run of -benchtime=5x has no warm-up run before it, so the first edit's one-time work (parsing the key identifiers a printable key is
			// compared with, compiling the regexps of a key sequence) fell in the measured iterations, divided by five: 312 allocs/op at 1x, 158 at 5x,
			// 138 at 10x and 120 at 200x for chars-32. One edit before the timer starts makes allocs/op what an edit costs, whichever benchmark ran first.
			edit()
			b.ReportAllocs()
			for b.Loop() {
				edit()
			}
		})
	}
}
