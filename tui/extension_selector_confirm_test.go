package tui

import "testing"

func handleSelectorInput(e *ExtensionSelectorComponent, data string) {
	(*ExtensionSelectorComponent).HandleInput(e, data)
}

// extension-selector.ts handleInput: a newline confirms like the confirm binding does, a selector with no options is not completed by it, and a countdown that expires after the
// user already chose does not cancel (the done guard in front of onCancel); the keys are consumed once the selector is done.
func TestExtensionSelectorComponentNewlineConfirmsEmptyOptionsStayOpenAndExpiryAfterSelectionIsIgnored(t *testing.T) {
	var picked []string
	cancelled := 0
	e := NewExtensionSelectorComponent("Pick", []string{"a", "b"}, func(option string) { picked = append(picked, option) }, func() { cancelled++ })
	handleSelectorInput(e, "j")
	handleSelectorInput(e, "\n")
	if len(picked) != 1 || picked[0] != "b" || !e.Done() || e.Cancelled() {
		t.Fatalf("a newline picked %v (done %v, cancelled %v), want b", picked, e.Done(), e.Cancelled())
	}
	e.expire()
	handleSelectorInput(e, "\n")
	if cancelled != 0 || e.Cancelled() || len(picked) != 1 {
		t.Errorf("an expiry or key after the choice cancelled %d times (cancelled %v) and picked %v", cancelled, e.Cancelled(), picked)
	}

	empty := NewExtensionSelectorComponent("Nothing", nil, func(string) { t.Error("an empty selector selected an option") }, func() {})
	handleSelectorInput(empty, "\n")
	if empty.Done() {
		t.Error("Enter on a selector without options completed it")
	}
}
