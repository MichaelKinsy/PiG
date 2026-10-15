package tui

import "testing"

// packages/tui/src/tui.ts:778-783 OverlayHandle.unfocus: `if (!isFocused && !hasPendingRestore) return;` before anything else, so unfocus() or unfocus({ target })
// on an overlay that does not hold focus and has no pending restore changes nothing: focus stays where it is and an explicit target is not focused.
// mutation-checked: inverting `!isFocused` at overlay_model.go unfocus lets an unfocused overlay move focus to the explicit target and fails it.
func TestOverlayHandleUnfocusIsANoOpWhenTheOverlayHasNoFocusToRelease(t *testing.T) {
	terminal := newNCUpstreamTerminal(80, 24)
	tui := terminal.newTUI(t)
	editor := &ncUpstreamComponent{lines: []string{"EDITOR"}}
	passive := &ncUpstreamComponent{lines: []string{"PASSIVE"}}
	other := &ncUpstreamComponent{lines: []string{"OTHER"}}
	tui.Add(&recordingComponent{})
	tui.SetFocus(editor)
	tui.Render()
	handle := tui.ShowOverlay(passive, OverlayOptions{nonCapturing: true})
	tui.ShowOverlay(other, OverlayOptions{nonCapturing: true})
	ncEqual(t, handle.IsFocused(), false)

	handle.UnfocusWith(OverlayUnfocusOptions{Target: other})
	handle.Unfocus()
	terminal.sendInput("x")
	terminal.flush(t)
	ncInputsEqual(t, editor.inputs, []string{"x"})
	ncInputsEqual(t, other.inputs, []string{})
	ncInputsEqual(t, passive.inputs, []string{})
}
