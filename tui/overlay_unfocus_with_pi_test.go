package tui

import "testing"

// TestOverlayHandleUnfocusWithMatchesPiUnfocusOptions drives the public
// OverlayHandle.UnfocusWith (Pi's OverlayHandle.unfocus(options),
// packages/tui/src/tui.ts:308 and :778-808) with the inputs of
// packages/tui/test/overlay-non-capturing.test.ts: explicit target cycling
// (:983), explicit null target (:1027) and the blocked-overlay release (:515).
// The zero-argument Unfocus keeps Pi's unfocus() fallback to the previous target.
func TestOverlayHandleUnfocusWithMatchesPiUnfocusOptions(t *testing.T) {
	t.Run("explicit target is focused instead of the fallback", func(t *testing.T) {
		terminal := newNCUpstreamTerminal(80, 24)
		tui := terminal.newTUI(t)
		editor := &ncUpstreamComponent{lines: []string{"EDITOR"}}
		a := &ncUpstreamComponent{lines: []string{"A"}}
		b := &ncUpstreamComponent{lines: []string{"B"}}
		tui.Add(&recordingComponent{})
		tui.SetFocus(editor)
		tui.Render()
		aHandle := tui.ShowOverlay(a, OverlayOptions{})
		tui.ShowOverlay(b, OverlayOptions{})
		aHandle.Focus()
		terminal.sendInput("a")
		terminal.flush(t)
		aHandle.UnfocusWith(OverlayUnfocusOptions{Target: b})
		terminal.sendInput("x")
		terminal.flush(t)
		ncInputsEqual(t, a.inputs, []string{"a"})
		ncInputsEqual(t, b.inputs, []string{"x"})
		ncInputsEqual(t, editor.inputs, []string{})
	})
	t.Run("zero-argument Unfocus restores the previous target", func(t *testing.T) {
		terminal := newNCUpstreamTerminal(80, 24)
		tui := terminal.newTUI(t)
		editor := &ncUpstreamComponent{lines: []string{"EDITOR"}}
		a := &ncUpstreamComponent{lines: []string{"A"}}
		tui.Add(&recordingComponent{})
		tui.SetFocus(editor)
		tui.Render()
		aHandle := tui.ShowOverlay(a, OverlayOptions{})
		aHandle.Focus()
		aHandle.Unfocus()
		terminal.sendInput("x")
		terminal.flush(t)
		ncInputsEqual(t, a.inputs, []string{})
		ncInputsEqual(t, editor.inputs, []string{"x"})
	})
	t.Run("explicit nil target clears focus without restoring overlays", func(t *testing.T) {
		terminal := newNCUpstreamTerminal(80, 24)
		tui := terminal.newTUI(t)
		editor := &ncUpstreamComponent{lines: []string{"EDITOR"}}
		overlay := &ncUpstreamComponent{lines: []string{"OVERLAY"}}
		tui.Add(&recordingComponent{})
		tui.SetFocus(editor)
		tui.Render()
		handle := tui.ShowOverlay(overlay, OverlayOptions{})
		handle.UnfocusWith(OverlayUnfocusOptions{Target: nil})
		terminal.sendInput("x")
		terminal.flush(t)
		ncInputsEqual(t, overlay.inputs, []string{})
		ncInputsEqual(t, editor.inputs, []string{})
		ncEqual(t, handle.IsFocused(), false)
	})
	t.Run("target releases a blocked overlay while the replacement stays focused", func(t *testing.T) {
		terminal := newNCUpstreamTerminal(80, 24)
		tui := terminal.newTUI(t)
		fallback := &ncUpstreamComponent{lines: []string{"FALLBACK"}}
		target := &ncUpstreamComponent{lines: []string{"TARGET"}}
		replacement := &ncUpstreamComponent{lines: []string{"REPLACEMENT"}}
		overlay := &ncUpstreamComponent{lines: []string{"OVERLAY"}}
		replacement.onInput = func(data string) {
			replacement.inputs = append(replacement.inputs, data)
			if data == "\r" {
				tui.SetFocus(fallback)
			}
		}
		tui.Add(&recordingComponent{})
		tui.Render()
		overlayHandle := tui.ShowOverlay(overlay, OverlayOptions{})
		overlay.onInput = func(data string) {
			overlay.inputs = append(overlay.inputs, data)
			if data == "b" {
				tui.SetFocus(replacement)
				overlayHandle.UnfocusWith(OverlayUnfocusOptions{Target: target})
			}
		}
		terminal.sendInput("b")
		terminal.flush(t)
		ncEqual(t, replacement.focused, true)
		terminal.sendInput("\r")
		terminal.sendInput("x")
		terminal.flush(t)
		ncInputsEqual(t, overlay.inputs, []string{"b"})
		ncInputsEqual(t, replacement.inputs, []string{"\r"})
		ncInputsEqual(t, fallback.inputs, []string{})
		ncInputsEqual(t, target.inputs, []string{"x"})
	})
}
