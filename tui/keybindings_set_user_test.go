package tui

import (
	"slices"
	"testing"
)

// PiG-only: upstream's keybinding tests build a manager with user bindings but never call setUserBindings afterwards.
func TestTUIKeybindingsManagerSetUserBindingsReplacesOverridesAndRebuilds(t *testing.T) {
	kb := NewTUIKeybindingsManager(nil)
	if !kb.Matches("\x1b", KBSelectCancel) {
		t.Fatal("escape must cancel by default")
	}
	kb.SetUserBindings(map[string][]string{KBSelectCancel: {"ctrl+q"}})
	if kb.Matches("\x1b", KBSelectCancel) || !slices.Equal(kb.GetKeys(KBSelectCancel), []string{"ctrl+q"}) {
		t.Fatalf("override not applied: keys %v", kb.GetKeys(KBSelectCancel))
	}
	if got := kb.GetUserBindings(); !slices.Equal(got[KBSelectCancel], []string{"ctrl+q"}) {
		t.Fatalf("user bindings = %v", got)
	}
	kb.SetUserBindings(map[string][]string{KBSelectUp: {}})
	if !kb.Matches("\x1b", KBSelectCancel) {
		t.Fatal("a second call must replace the first call's overrides, not merge")
	}
	if kb.Matches("\x1b[A", KBSelectUp) {
		t.Fatal("an explicit empty binding disables the default")
	}
	kb.SetUserBindings(nil)
	if !kb.Matches("\x1b[A", KBSelectUp) || len(kb.GetUserBindings()) != 0 {
		t.Fatal("nil must clear every override")
	}
}
