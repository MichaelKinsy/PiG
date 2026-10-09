package tui

import (
	"io"
	"testing"
)

// keys.ts:505 KeyEventType is the closed union "press" | "repeat" | "release".
func TestKeyEventTypeValues(t *testing.T) {
	for got, want := range map[KeyEventType]string{KeyEventPress: "press", KeyEventRepeat: "repeat", KeyEventRelease: "release"} {
		if string(got) != want {
			t.Errorf("KeyEventType %q, want %q", got, want)
		}
	}
}

// tui.ts:186 isFocusable and tui.ts:491 isViewportTUI.
func TestIsFocusableAndIsViewportTUI(t *testing.T) {
	if IsFocusable(nil) {
		t.Error("nil is not focusable")
	}
	if IsFocusable(NewText("x")) {
		t.Error("a Text has no focused flag")
	}
	if !IsFocusable(NewEditor()) {
		t.Error("an Editor holds the focused flag")
	}
	if IsViewportTUI(nil) {
		t.Error("a nil renderer has no viewport")
	}
	if !IsViewportTUI(NewTuiAltScreenWithOutput(io.Discard, 80, 24, TuiAltScreenOptions{})) {
		t.Error("the alt-screen renderer owns a viewport")
	}
}
