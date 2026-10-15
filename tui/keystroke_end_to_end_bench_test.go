package tui

import (
	"strings"
	"testing"
)

// BenchmarkEditorPrintableKeystroke is one printable key typed into an editor that holds a 40-line draft and rendered into a frame, the path a keystroke takes from the input handler to the lines the terminal draws. The backspace that restores the draft is not rendered, so each iteration starts from the same text.
func BenchmarkEditorPrintableKeystroke(b *testing.B) {
	editor := NewEditor()
	editor.SetText(strings.Repeat("a line of the draft with several words in it\n", 40))
	editor.Render(100)
	letters := "abcdefghij"
	b.ReportAllocs()
	i := 0
	for b.Loop() {
		editor.HandleInput(letters[i%len(letters) : i%len(letters)+1])
		editor.Render(100)
		editor.HandleInput("\x7f")
		i++
	}
}
