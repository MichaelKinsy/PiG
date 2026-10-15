package codingagent

import (
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// Pi 0.87.1 interactive-mode showExtensionCustom opens ctx.ui.custom(..., {overlay: true}) with tui.showOverlay, whose
// setFocus clears the editor's focused flag (tui.ts:605-612). The editor then emits no CURSOR_MARKER, and the one
// marker the extension's focused input renders is stripped, so the terminal never receives "\x1b_pi:c\a". The
// Windows console prints that unknown APC sequence as "pi:c" and wraps the editor row.
func TestRemoteOverlayLeavesNoCursorMarkerInTerminalOutput(t *testing.T) {
	// RunRemoteOverlay renders on its own goroutine after the handle opens, while this test resets and reads the output.
	var out synchronizedOutput
	ui := tui.NewWithOutput(&out, 100, 30)
	t.Cleanup(ui.CancelPendingRender)
	editor := tui.NewEditor()
	editor.SetText("draft")
	editorContainer := tui.NewContainer()
	editorContainer.SetChildren(editor)
	ui.Add(editorContainer)
	m := &InteractiveMode{tuiInst: ui, layout: tui.NewContainer(), editorContainer: editorContainer, editor: editor}
	ui.SetFocus(editor)
	ui.Render()
	if !editor.Focused {
		t.Fatal("SetFocus(editor) left the editor unfocused")
	}

	u := &ExtUIContext{m: m}
	handleCh := make(chan extension.RemoteOverlayHandle, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		u.RunRemoteOverlay(extension.RemoteOverlayOptions{Overlay: true}, nil, func(h extension.RemoteOverlayHandle) { handleCh <- h })
	}()
	var handle extension.RemoteOverlayHandle
	select {
	case handle = <-handleCh:
	case <-time.After(5 * time.Second):
		t.Fatal("overlay never opened")
	}
	handle.UpdateLines([]string{"Overlay Test", "Search: " + widthx.CursorMarker})

	out.Reset()
	ui.ForceFullRender()
	ui.Render()
	if editor.Focused {
		t.Fatal("the editor kept focus under a focused extension overlay")
	}
	if output := out.String(); strings.Contains(output, widthx.CursorMarker) {
		t.Fatalf("terminal output carries the cursor marker: %q", output)
	}
	if output := out.String(); !strings.Contains(output, "Search:") {
		t.Fatalf("overlay was not rendered: %q", output)
	}

	handle.Close(nil)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("overlay never closed")
	}
	if !editor.Focused {
		t.Fatal("closing the overlay did not return focus to the editor")
	}
}
