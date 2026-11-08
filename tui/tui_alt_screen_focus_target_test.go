package tui

import "testing"

// TestAltScreenResolveMouseFocusTargetKeepsTheClickOnTheFocusOwnerExceptForOverlays pins Go's form of overriding upstream's protected
// TuiBase.resolveMouseFocusTarget (p2's RuntimeAltScreen): `target === component ? focusOwner ?? target : target`. A click that pi-tui answers with
// `focus: true` on a plain layout component sends focus to the app's focus owner (the owner keeps getting every key, so Escape, Ctrl+C and the
// commands are not bypassed by a focused editor); a click inside an overlay keeps the overlay as the base resolved it.
func TestAltScreenResolveMouseFocusTargetKeepsTheClickOnTheFocusOwnerExceptForOverlays(t *testing.T) {
	owner := &inputRecorder{lines: []string{"owner"}}
	h := newAltHarness(t, 20, 6, TuiAltScreenOptions{ResolveMouseFocusTarget: func(component, resolved Component) Component {
		if resolved == component {
			return owner
		}
		return resolved
	}})
	editor := NewInput(InputOptions{})
	editor.SetText("hi")
	h.tui.SetLayoutRoot(NewVStack([]StackEntry{{Component: editor}}, StackOptions{}))
	h.tui.SetFocus(owner)
	h.start()
	h.render()
	// A click on the editor: pi-tui would focus the editor itself; the owner keeps the keys.
	h.send("\x1b[<0;2;1M", "\x1b[<0;2;1m", "!")
	if h.tui.GetFocusedComponent() != owner || editor.Text() != "hi" || len(owner.inputs) == 0 || owner.inputs[len(owner.inputs)-1] != "!" {
		t.Fatalf("after an editor click: focused=%v editor=%q owner inputs=%q", h.tui.GetFocusedComponent(), editor.Text(), owner.inputs)
	}
	// A focus result for a control nested inside an overlay: the base resolves it to the overlay container (resolved != component), which the owner
	// is not given.
	input := NewInput(InputOptions{})
	input.SetText("hi")
	overlay := NewContainer(input)
	h.tui.ShowOverlay(overlay, OverlayOptions{anchor: overlayTopLeft, width: overlayCells(20)})
	h.render()
	result := &TuiMouseDispatchResult{TuiMouseEventResult: TuiMouseEventResult{Handled: true, Focus: true}, Target: TuiMouseDispatchTarget{Component: input}}
	h.tui.applyMouseDispatchResult(TuiMouseEvent{Type: MousePress}, result)
	if h.tui.GetFocusedComponent() != Component(overlay) {
		t.Fatalf("after a nested overlay control's focus result: focused=%v", h.tui.GetFocusedComponent())
	}
}
