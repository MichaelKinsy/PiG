package codingagent

import (
	"io"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// upstream: interactive-mode.ts:4578 (clearEditor): the editor text is emptied and the screen re-rendered; Ctrl+C reaches it through the clear outcome (handleCtrlC).
func TestClearEditorEmptiesTheEditor(t *testing.T) {
	editor := tui.NewEditor()
	editor.SetText("draft message")
	mode := &InteractiveMode{editor: editor, tuiInst: tui.NewWithOutput(io.Discard, 80, 24)}
	mode.ClearEditor()
	if editor.Text() != "" {
		t.Fatalf("editor text = %q after ClearEditor", editor.Text())
	}
	// Clearing an empty editor is harmless and idempotent.
	mode.ClearEditor()
	if editor.Text() != "" {
		t.Fatalf("editor text = %q after a second ClearEditor", editor.Text())
	}
}
