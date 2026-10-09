package tui

import (
	"bytes"
	"slices"
	"strings"
	"testing"
)

// tui.ts TUI (tui.ts:453-480): the interface the driver holds mounts children in order with addChild, drops one with
// removeChild, empties them with clear, lists them in children, closes the topmost overlay with hideOverlay, renders at once
// with renderNow, and reports the two cursor and shrink preferences it was given.
// mutation-checked: zeroing the results of Container.Children, tuiBase.GetShowHardwareCursor, tuiBase.GetClearOnShrink fails it
// Pi: packages/tui/src/tui.ts:455 (children)
// Pi: packages/tui/src/tui.ts:459 (addChild)
// Pi: packages/tui/src/tui.ts:460 (removeChild)
// Pi: packages/tui/src/tui.ts:461 (clear)
// Pi: packages/tui/src/tui.ts:462 (getShowHardwareCursor)
// Pi: packages/tui/src/tui.ts:464 (getClearOnShrink)
// Pi: packages/tui/src/tui.ts:468 (hideOverlay)
// Pi: packages/tui/src/tui.ts:472 (renderNow)
func TestTUIInterfaceMountsChildrenInOrderAndReportsItsPreferences(t *testing.T) {
	main, render := upstreamOverlayScreen(40, 6)
	alt := NewTuiAltScreenWithOutput(new(bytes.Buffer), 40, 6, TuiAltScreenOptions{})
	// Each screen is checked through its own viewport, in a fixed order: the alt screen's lines come from its own snapshot.
	screens := []struct {
		name   string
		ui     TUI
		render func() []string
	}{
		{"main screen", main, render},
		{"alt screen", alt, func() []string { return alt.RenderSnapshot(40) }},
	}
	for _, screen := range screens {
		ui, render := screen.ui, screen.render
		t.Run(screen.name, func(t *testing.T) {
			a, b, c := &recordingComponent{lines: []string{"A"}}, &recordingComponent{lines: []string{"B"}}, &recordingComponent{lines: []string{"C"}}
			ui.AddChild(a)
			ui.AddChild(b)
			ui.AddChild(c)
			if got := ui.Children(); !slices.Equal(got, []Component{a, b, c}) {
				t.Fatalf("children after addChild = %v", got)
			}
			ui.RemoveChild(b)
			if got := ui.Children(); !slices.Equal(got, []Component{a, c}) {
				t.Fatalf("children after removeChild = %v", got)
			}
			if viewport := render(); !strings.Contains(strings.Join(viewport, "\n"), "A") || strings.Contains(strings.Join(viewport, "\n"), "B") {
				t.Fatalf("viewport %q", viewport)
			}
			ui.Clear()
			if got := ui.Children(); len(got) != 0 {
				t.Fatalf("children after clear = %v", got)
			}
			ui.SetShowHardwareCursor(true)
			ui.SetClearOnShrink(true)
			if !ui.GetShowHardwareCursor() || !ui.GetClearOnShrink() {
				t.Fatal("the preferences were not reported as set")
			}
			ui.SetShowHardwareCursor(false)
			ui.SetClearOnShrink(false)
			if ui.GetShowHardwareCursor() || ui.GetClearOnShrink() {
				t.Fatal("the preferences were not reported as cleared")
			}
			ui.ShowOverlay(&recordingComponent{lines: []string{"OVER"}}, OverlayOptions{anchor: overlayTopLeft, width: overlayCells(10)})
			if !ui.HasOverlay() {
				t.Fatal("the overlay did not open")
			}
			ui.HideOverlay()
			if ui.HasOverlay() {
				t.Fatal("hideOverlay left the overlay open")
			}
			ui.AddChild(a)
			ui.RenderNow(true)
		})
	}
}
