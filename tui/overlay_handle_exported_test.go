package tui

import (
	"io"
	"testing"
)

// PiG-only: the upstream overlay tests drive the handle's lowercase internals; this one drives the exported methods an
// extension reaches through OverlayHandle (focus, unfocus, setHidden, isHidden, isFocused).
func TestOverlayHandleExportedMethodsToggleFocusAndVisibility(t *testing.T) {
	tu := NewWithOutput(io.Discard, 80, 24)
	first := &recordingComponent{lines: []string{"first"}}
	second := &recordingComponent{lines: []string{"second"}}
	firstHandle := tu.ShowOverlay(first, OverlayOptions{})
	secondHandle := tu.ShowOverlay(second, OverlayOptions{})
	if firstHandle.IsFocused() || !secondHandle.IsFocused() || secondHandle.IsHidden() {
		t.Fatal("a newly opened overlay must hold focus and be visible")
	}
	secondHandle.SetHidden(true)
	if !secondHandle.IsHidden() || secondHandle.IsFocused() || tu.ActiveOverlay() != first {
		t.Fatal("hiding the focused overlay must hand focus to the next visible one")
	}
	secondHandle.Focus()
	if secondHandle.IsFocused() {
		t.Fatal("a hidden overlay accepted focus")
	}
	secondHandle.SetHidden(false)
	secondHandle.Focus()
	if !secondHandle.IsFocused() || firstHandle.IsFocused() {
		t.Fatal("Focus did not move focus to the shown overlay")
	}
	secondHandle.Unfocus()
	if secondHandle.IsFocused() {
		t.Fatal("Unfocus left the overlay focused")
	}
	secondHandle.Focus()
	if !secondHandle.IsFocused() {
		t.Fatal("Focus did not restore focus after Unfocus")
	}
	firstHandle.Close()
	secondHandle.Close()
}
