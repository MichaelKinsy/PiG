package tui

import "testing"

type focusProbe struct {
	invalidatable
	focused bool
}

func (p *focusProbe) Render(int) []string { return nil }
func (p *focusProbe) SetFocused(v bool)   { p.focused = v }

// Pi tui.ts:186 isFocusable: false for null and for a component without `focused`, true otherwise; setFocus (tui.ts:638-646) moves the flag through it.
func TestIsFocusableMatchesUpstreamGuard(t *testing.T) {
	if IsFocusable(nil) {
		t.Fatal("nil is not focusable")
	}
	if IsFocusable(NewText("plain")) {
		t.Fatal("a Text carries no focused state")
	}
	p := &focusProbe{}
	if !IsFocusable(p) {
		t.Fatal("a component with SetFocused is focusable")
	}
	q := &focusProbe{}
	moveFocusFlag(p, q)
	if p.focused || !q.focused {
		t.Fatalf("focus flag did not move: p=%v q=%v", p.focused, q.focused)
	}
}
