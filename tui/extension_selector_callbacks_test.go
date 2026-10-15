package tui

import "testing"

// upstream: modes/interactive/components/extension-selector.ts:30-41,96-111 the constructor takes onSelect and onCancel;
// confirm calls onSelect(option) for the highlighted option and cancel calls onCancel.
func TestExtensionSelectorCallsOnSelectAndOnCancel(t *testing.T) {
	var selected []string
	cancels := 0
	e := NewExtensionSelectorComponent("Pick", []string{"a", "b", "c"},
		func(option string) { selected = append(selected, option) }, func() { cancels++ })
	e.HandleInput("\x1b[B")
	e.HandleInput("\r")
	if len(selected) != 1 || selected[0] != "b" || cancels != 0 {
		t.Fatalf("after down+enter: selected %v cancels %d, want [b] and 0", selected, cancels)
	}
	c := NewExtensionSelectorComponent("Pick", []string{"a"}, func(string) { t.Fatal("onSelect on cancel") }, func() { cancels++ })
	c.HandleInput("\x1b")
	if cancels != 1 {
		t.Fatalf("escape called onCancel %d times, want 1", cancels)
	}
	d := NewExtensionSelectorComponent("Pick", []string{"a"}, nil, func() { cancels++ })
	d.expire()
	if cancels != 2 {
		t.Fatalf("countdown expiry (extension-selector.ts:61) called onCancel: total %d, want 2", cancels)
	}
}
