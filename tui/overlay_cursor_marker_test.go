package tui

import (
	"bytes"
	"testing"

	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// Pi 0.87.1 TUI.setFocus clears the previous component's focused flag and sets the next one's (tui.ts:605-612). An
// Editor under a focused overlay therefore emits no CURSOR_MARKER, and extractCursorPosition strips the one marker the
// overlay's own input emits, so no marker reaches the terminal. A terminal without APC support, such as the Windows
// console, would otherwise print "pi:c" and wrap the editor row.
func TestFocusedOverlayLeavesNoCursorMarkerInTerminalOutput(t *testing.T) {
	for _, tc := range []struct {
		name string
		open func(ui *TuiMainScreen, overlay Component) *OverlayHandle
	}{
		{"capturing overlay", func(ui *TuiMainScreen, overlay Component) *OverlayHandle {
			return ui.ShowOverlay(overlay, OverlayOptions{})
		}},
		{"focused non-capturing overlay", func(ui *TuiMainScreen, overlay Component) *OverlayHandle {
			handle := ui.ShowOverlay(overlay, OverlayOptions{nonCapturing: true})
			handle.focus()
			return handle
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			ui := newManualRenderTUI(&out, 80, 24)
			t.Cleanup(ui.CancelPendingRender)
			editor := NewEditor()
			editor.SetText("draft")
			ui.Add(editor)
			ui.SetFocus(editor)
			ui.Render()
			if !editor.Focused {
				t.Fatal("SetFocus(editor) left the editor unfocused")
			}
			overlay := &ncUpstreamComponent{lines: []string{"Search: " + widthx.CursorMarker}}
			handle := tc.open(ui, overlay)
			out.Reset()
			ui.ForceFullRender()
			ui.Render()
			if bytes.Contains(out.Bytes(), []byte(widthx.CursorMarker)) {
				t.Fatalf("terminal output carries the cursor marker: %q", out.String())
			}
			if editor.Focused || !overlay.focused {
				t.Fatalf("editor focused=%v overlay focused=%v, want false/true", editor.Focused, overlay.focused)
			}

			handle.Close()
			out.Reset()
			ui.ForceFullRender()
			ui.Render()
			if !editor.Focused || overlay.focused {
				t.Fatalf("after close: editor focused=%v overlay focused=%v, want true/false", editor.Focused, overlay.focused)
			}
			if bytes.Contains(out.Bytes(), []byte(widthx.CursorMarker)) {
				t.Fatalf("terminal output after close carries the cursor marker: %q", out.String())
			}
		})
	}
}
