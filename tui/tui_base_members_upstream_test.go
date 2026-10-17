package tui

import (
	"bytes"
	"testing"
)

// Ports packages/tui/src/tui.ts:1074-1078 (TuiBase.handleInput) and the onDebug property (tui.ts:457, :502): the global debug key
// (Shift+Ctrl+D) calls onDebug and consumes the input; without a callback the key is not consumed, and the property reads back
// what was set, as interactive-mode.ts:900,920 copies it to a replacement renderer.
func TestDebugKeyCallsOnDebug(t *testing.T) {
	for name, renderer := range map[string]TUI{
		"main": NewWithOutput(&bytes.Buffer{}, 40, 10),
		"alt":  NewTuiAltScreenWithOutput(&bytes.Buffer{}, 40, 10, TuiAltScreenOptions{}),
	} {
		t.Run(name, func(t *testing.T) {
			const debugKey = "\x1b[100;6u" // kitty CSI-u: d with shift+ctrl
			if renderer.OnDebug() != nil {
				t.Fatal("onDebug is set on a new renderer")
			}
			if renderer.ConsumeDebugKey(debugKey) {
				t.Fatal("debug key consumed without an OnDebug callback")
			}
			calls := 0
			renderer.SetOnDebug(func() { calls++ })
			if got := renderer.OnDebug(); got == nil {
				t.Fatal("OnDebug does not return the callback that was set")
			} else if got(); calls != 1 {
				t.Fatalf("the callback OnDebug returns is not the one that was set (calls = %d)", calls)
			} else {
				calls = 0
			}
			if renderer.ConsumeDebugKey("x") || calls != 0 {
				t.Fatalf("ordinary input consumed, calls = %d", calls)
			}
			if !renderer.ConsumeDebugKey(debugKey) || calls != 1 {
				t.Fatalf("debug key consumed = false or calls = %d", calls)
			}
			renderer.SetOnDebug(nil)
			if renderer.OnDebug() != nil {
				t.Fatal("OnDebug still returns a callback after it was cleared")
			}
			if renderer.ConsumeDebugKey(debugKey) {
				t.Fatal("debug key consumed after the callback was cleared")
			}
		})
	}
}

// Ports tui.ts TuiBase.getClearOnShrink/setClearOnShrink: the preference is stored on the base, so the alt-screen
// renderer reports what was set even though only the inline renderer acts on it. Both are reached through the public TUI
// interface, as Pi's TUI.getClearOnShrink is public.
func TestClearOnShrinkIsStoredOnBothRenderers(t *testing.T) {
	for name, renderer := range map[string]TUI{
		"main": NewWithOutput(&bytes.Buffer{}, 40, 10),
		"alt":  NewTuiAltScreenWithOutput(&bytes.Buffer{}, 40, 10, TuiAltScreenOptions{}),
	} {
		t.Run(name, func(t *testing.T) {
			if renderer.GetClearOnShrink() {
				t.Fatal("clearOnShrink defaults to true")
			}
			renderer.SetClearOnShrink(true)
			if !renderer.GetClearOnShrink() {
				t.Fatal("SetClearOnShrink(true) was not stored")
			}
		})
	}
}

// Ports tui.ts ViewportTUI extends TUI: a ViewportTUI value reaches every inherited member. The alt-screen renderer
// mounts children, overlays and input listeners, reports the preferences it was given, and renderNow(force) emits a
// full redraw.
// Pi: packages/tui/src/tui.ts:475 (removeInputListener).
// Pi: packages/tui/src/tui.ts:459 (addChild).
// Pi: packages/tui/src/tui.ts:464 (getClearOnShrink).
// Pi: packages/tui/src/tui.ts:468 (hideOverlay).
// Pi: packages/tui/src/tui.ts:460 (removeChild).
func TestViewportTUIInheritedMembers(t *testing.T) {
	altScreen := NewTuiAltScreenWithOutput(&bytes.Buffer{}, 40, 10, TuiAltScreenOptions{})
	// tui-alt-screen.ts:203 readonly mode = "fullscreen".
	if altScreen.Mode() != TuiModeFullscreen {
		t.Fatalf("TuiAltScreen.Mode() = %q", altScreen.Mode())
	}
	var viewport ViewportTUI = altScreen
	viewport.SetRenderDispatcher(func(func()) {})
	if viewport.Mode() != "fullscreen" {
		t.Fatalf("mode = %q", viewport.Mode())
	}

	first, second := NewText("one"), NewText("two")
	viewport.AddChild(first)
	viewport.AddChild(second)
	if children := viewport.Children(); len(children) != 2 || children[0] != Component(first) {
		t.Fatalf("children after AddChild = %v", children)
	}
	viewport.RemoveChild(first)
	if children := viewport.Children(); len(children) != 1 || children[0] != Component(second) {
		t.Fatalf("children after RemoveChild = %v", children)
	}
	viewport.Clear()
	if len(viewport.Children()) != 0 {
		t.Fatalf("children after Clear = %v", viewport.Children())
	}

	if viewport.GetShowHardwareCursor() {
		t.Fatal("hardware cursor shown by default")
	}
	viewport.SetShowHardwareCursor(true)
	if !viewport.GetShowHardwareCursor() {
		t.Fatal("SetShowHardwareCursor(true) not reported")
	}
	viewport.SetClearOnShrink(true)
	if !viewport.GetClearOnShrink() {
		t.Fatal("SetClearOnShrink(true) not reported")
	}

	overlay := NewText("overlay")
	viewport.ShowOverlay(overlay, OverlayOptions{})
	if !viewport.HasOverlay() {
		t.Fatal("ShowOverlay did not mount")
	}
	viewport.HideOverlay()
	if viewport.HasOverlay() {
		t.Fatal("HideOverlay left the overlay mounted")
	}

	var received []string
	listener := TuiInputListener(func(data string) *TuiInputResult { received = append(received, data); return nil })
	remove := viewport.AddInputListener(&listener)
	viewport.RunInputListeners("a")
	remove()
	viewport.RunInputListeners("b")
	viewport.AddInputListener(&listener)
	viewport.RemoveInputListener(&listener)
	viewport.RunInputListeners("c")
	if len(received) != 1 || received[0] != "a" {
		t.Fatalf("listener received %v, want only the input before removal", received)
	}

}

// Ports tui.ts TuiBase.onDebug (tui.ts:457,502): the property reads back what was assigned and is undefined (nil) after
// being cleared; the debug key runs the stored callback.
func TestOnDebugReadsBackTheStoredCallback(t *testing.T) {
	for name, renderer := range map[string]TUI{
		"main": NewWithOutput(&bytes.Buffer{}, 40, 10),
		"alt":  NewTuiAltScreenWithOutput(&bytes.Buffer{}, 40, 10, TuiAltScreenOptions{}),
	} {
		t.Run(name, func(t *testing.T) {
			if renderer.OnDebug() != nil {
				t.Fatal("OnDebug is set on a new renderer")
			}
			calls := 0
			renderer.SetOnDebug(func() { calls++ })
			stored := renderer.OnDebug()
			if stored == nil {
				t.Fatal("OnDebug is nil after SetOnDebug")
			}
			stored()
			if calls != 1 {
				t.Fatalf("the stored callback is not the one that was set (calls = %d)", calls)
			}
			renderer.SetOnDebug(nil)
			if renderer.OnDebug() != nil {
				t.Fatal("OnDebug is set after SetOnDebug(nil)")
			}
		})
	}
}

// Ports tui.ts:372-392 Container.handleMouse, inherited by ViewportTUI through TUI: an event row inside the container reaches the
// child that rendered that row with the event translated to the child's rows; a row outside the rendered rows is undefined.
func TestViewportTUIHandleMouseRoutesToChildUnderPointer(t *testing.T) {
	var viewport ViewportTUI = NewTuiAltScreenWithOutput(&bytes.Buffer{}, 40, 10, TuiAltScreenOptions{})
	first := &mouseProbe{lines: []string{"a", "b"}, result: &TuiMouseEventResult{Handled: true}}
	second := &mouseProbe{lines: []string{"c"}, result: &TuiMouseEventResult{Handled: true}}
	viewport.AddChild(first)
	viewport.AddChild(second)

	result := viewport.HandleMouse(leftMouse(MousePress, 3, 2, 10, 3))
	if result == nil || result.Target.Component != Component(second) || len(second.events) != 1 || len(first.events) != 0 {
		t.Fatalf("row 2 must reach only the second child: %+v", result)
	}
	if got := second.events[0]; got.Y != 0 || got.Height != 1 || got.X != 3 || got.ScreenY != 2 {
		t.Fatalf("child event = %+v, want y 0 of a one-row child", got)
	}
	if viewport.HandleMouse(leftMouse(MousePress, 0, 3, 10, 3)) != nil {
		t.Fatal("a row at or past the event height is undefined")
	}
	if viewport.HandleMouse(leftMouse(MousePress, 0, -1, 10, 3)) != nil {
		t.Fatal("a negative row is undefined")
	}
}
