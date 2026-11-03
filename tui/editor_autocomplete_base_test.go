package tui

import "testing"

// A custom editor component owns the host's view of the editor: while it is installed, the host's AutocompleteOpen answers false, and the
// component reaches the editor's own menu through AsBase, Pi's `super.isShowingAutocomplete()` (custom-editor.ts handleInput).
// Installing a remote closes an open menu.
func TestEditorAutocompleteOpenIsTheBaseEditorsMenuAndTheRemoteOwnsTheHostView(t *testing.T) {
	editor := NewEditor()
	editor.SetAutocomplete(commandProvider{})
	editor.HandleInput("/")
	if !editor.AutocompleteOpen() {
		t.Fatal("the menu did not open on /")
	}
	editor.SetDelegatedRemote(&recordingRemote{})
	editor.AsBase(func() {
		if editor.AutocompleteOpen() {
			t.Error("installing a remote did not close the open menu")
		}
		editor.HandleInput("/")
		if !editor.AutocompleteOpen() {
			t.Error("the base editor's menu did not open through AsBase")
		}
	})
	if editor.AutocompleteOpen() {
		t.Fatal("the host sees the base editor's menu while a remote owns the editor")
	}
	editor.AsBase(func() {
		editor.AutocompleteCancel()
		if editor.AutocompleteOpen() {
			t.Error("the base menu stayed open after AutocompleteCancel")
		}
	})
}
