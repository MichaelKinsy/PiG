package tui

import (
	"slices"
	"testing"
)

// Pi's setCustomEditorComponent builds a new Editor for the factory (interactive-mode.ts:2866) and copies only the
// text into it (newEditor.setText(currentText)), so the custom editor starts with an empty prompt history, an undo
// stack whose only entry is the empty document, no kill ring and no pastes. Prompts submitted while it is installed
// enter its own history (this.editor.addToHistory). Clearing the factory restores the default editor, untouched
// since the install, with defaultEditor.setText(currentText). A delegated component's base is this editor, so the
// install and the removal must swap that per-instance state.
func TestDelegatedRemoteBaseIsAFreshEditorInstance(t *testing.T) {
	e := NewEditor()
	e.AddToHistory("submitted before")
	e.killRing.Push("killed before", false, false)
	e.SetText("draft")

	e.SetDelegatedRemote(&recordingRemote{})
	if got := e.Text(); got != "draft" {
		t.Fatalf("text after install = %q; want the carried draft", got)
	}
	if len(e.inputHistory) != 0 || e.killRing.Len() != 0 || len(e.pastes) != 0 {
		t.Fatalf("custom editor inherited state: history=%q killRing=%d pastes=%d", e.inputHistory, e.killRing.Len(), len(e.pastes))
	}
	if len(e.history) != 1 || !slices.Equal(e.history[0].lines, []string{""}) {
		t.Fatalf("undo stack = %+v; want one snapshot of the empty document", e.history)
	}
	e.AsBase(func() { e.AddToHistory("submitted in custom") })

	e.SetRemote(nil)
	if !slices.Equal(e.inputHistory, []string{"submitted before"}) || e.killRing.Len() != 1 {
		t.Fatalf("restored default editor: history=%q killRing=%d; want its own state back", e.inputHistory, e.killRing.Len())
	}
	if got := e.Text(); got != "draft" {
		t.Fatalf("text after removal = %q; want the custom editor's text", got)
	}
}
