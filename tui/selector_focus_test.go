package tui

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// Upstream OAuthSelectorComponent and TreeSelectorComponent implement Focusable: `set focused` reaches the
// search input and the label input, so only a focused selector emits the hardware-cursor marker.
func TestOAuthSelectorFocusPropagatesToSearchInput(t *testing.T) {
	sel := NewOAuthSelectorComponent("login", []OAuthProvider{{ID: "a", Name: "A"}}, nil, nil)
	if !IsFocusable(sel) {
		t.Fatal("OAuthSelectorComponent is not Focusable")
	}
	has := func() bool { return strings.Contains(strings.Join(sel.Render(60), "\n"), widthx.CursorMarker) }
	if has() {
		t.Fatal("an unfocused selector emitted the cursor marker")
	}
	sel.SetFocused(true)
	if !has() {
		t.Fatal("a focused selector did not emit the cursor marker")
	}
	sel.SetFocused(false)
	if has() {
		t.Fatal("cursor marker stayed after focus was lost")
	}
}

func TestTreeSelectorFocusPropagatesToLabelInput(t *testing.T) {
	treeHelpTestKeybindings(t, nil)
	sel := NewTreeSelectorComponent("", &fakeNode{id: "root", kids: []TreeNode{&fakeNode{id: "a", label: "A"}}})
	if !IsFocusable(sel) {
		t.Fatal("TreeSelectorComponent is not Focusable")
	}
	has := func() bool { return strings.Contains(strings.Join(sel.Render(80), "\n"), widthx.CursorMarker) }

	// Label input created while the selector is unfocused starts unfocused (upstream: labelInput.focused = this._focused).
	sel.HandleInput("L")
	if sel.labelInput == nil {
		t.Fatal("shift+l did not open the label input")
	}
	if has() {
		t.Fatal("label input emitted the cursor marker while the selector was unfocused")
	}
	sel.SetFocused(true)
	if !has() {
		t.Fatal("focus did not reach the active label input")
	}
	sel.SetFocused(false)
	if has() {
		t.Fatal("cursor marker stayed after focus was lost")
	}

	// A label input opened while the selector holds focus inherits it.
	sel.labelInput = nil
	sel.SetFocused(true)
	sel.HandleInput("L")
	if !has() {
		t.Fatal("a label input opened under focus did not inherit it")
	}
}
