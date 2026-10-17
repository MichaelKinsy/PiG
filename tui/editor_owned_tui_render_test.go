package tui

import (
	"io"
	"testing"
	"time"
)

// An Editor that owns its TUI (editor.ts constructor(tui, ...)) reads terminal.rows while the renderer draws the frame, so the renderer's
// Terminal accessor must not wait for the lock the frame holds. Starting a real alternate-screen renderer with such an editor must draw.
func TestEditorOwnedByARealRendererDrawsInsideTheFrame(t *testing.T) {
	for _, tc := range []struct {
		name string
		make func() *TuiAltScreen
	}{
		{"alternate screen over a terminal", func() *TuiAltScreen {
			return NewTuiAltScreen(NewProcessTerminalWithOutput(nil, nil, io.Discard), nil, "")
		}},
		{"alternate screen with a fixed output", func() *TuiAltScreen {
			return NewTuiAltScreenWithOutput(io.Discard, 80, 24, TuiAltScreenOptions{})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ui := tc.make()
			editor := NewEditorWithTUI(ui, EditorTheme{BorderColor: func(s string) string { return s }}, EditorOptions{})
			ui.AddChild(editor)
			done := make(chan struct{})
			go func() {
				defer close(done)
				ui.Start()
			}()
			select {
			case <-done:
				ui.Stop()
			case <-time.After(10 * time.Second):
				t.Fatal("rendering a frame that holds an editor owned by the renderer deadlocked on the renderer's lock")
			}
		})
	}
}
