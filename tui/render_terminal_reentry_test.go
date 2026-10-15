package tui

import (
	"io"
	"testing"
	"time"
)

// editor.ts:536 reads this.tui.terminal.rows while the TUI renders. A component that asks its renderer for the terminal during Render must not wait on
// the lock the render holds: the first frame of interactive mode painted nothing because Editor.Render's Terminal() call blocked on it.
func TestEditorOwningATUIRendersWithoutWaitingOnTheRenderLock(t *testing.T) {
	for name, build := range map[string]func() TUI{
		"main screen": func() TUI { return NewWithOutput(io.Discard, 80, 40) },
		"alt screen":  func() TUI { return NewTuiAltScreenWithOutput(io.Discard, 80, 40, TuiAltScreenOptions{}) },
	} {
		t.Run(name, func(t *testing.T) {
			ui := build()
			editor := NewEditorWithTUI(ui, EditorTheme{}, EditorOptions{})
			ui.Add(editor)
			done := make(chan struct{})
			go func() {
				defer close(done)
				ui.Render()
			}()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("Render deadlocked: a component's Terminal() call waited on the lock the render holds")
			}
			if rows := ui.Terminal().Rows(); rows != 40 {
				t.Errorf("Terminal().Rows() = %d, want the fixed size 40", rows)
			}
		})
	}
}
