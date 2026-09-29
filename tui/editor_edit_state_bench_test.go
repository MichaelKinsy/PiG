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
			b.ReportAllocs()
			for b.Loop() {
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
		})
	}
}
