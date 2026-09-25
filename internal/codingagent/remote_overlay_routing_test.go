package codingagent

import (
	"io"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// TestRunRemoteOverlayHonorsOverlayFlag pins the routing contract that mirrors
// upstream ui.custom(): opts.Overlay=true opens a floating viewport overlay and
// leaves the editor slot intact, while opts.Overlay=false replaces the editor
// slot inline. Before this fix pig ignored the flag and always used the editor
// slot in the interactive layout, silently downgrading overlay: true.
func TestRunRemoteOverlayHonorsOverlayFlag(t *testing.T) {
	const marker = "OVERLAY_MARKER_LINE"

	openOverlay := func(t *testing.T, overlay bool) *tui.Container {
		t.Helper()
		editor := tui.NewEditor()
		editorContainer := tui.NewContainer()
		editorContainer.SetChildren(editor)
		// layout != nil means the interactive layout: the branch that, before
		// the fix, always used the editor slot regardless of the flag.
		m := &InteractiveMode{
			tuiInst:         tui.NewWithOutput(io.Discard, 120, 40),
			layout:          tui.NewContainer(),
			editorContainer: editorContainer,
			editor:          editor,
		}
		u := &ExtUIContext{m: m}

		handleCh := make(chan extension.RemoteOverlayHandle, 1)
		go func() {
			u.RunRemoteOverlay(
				extension.RemoteOverlayOptions{Title: "ask", Overlay: overlay},
				nil,
				func(h extension.RemoteOverlayHandle) { handleCh <- h },
			)
		}()

		var handle extension.RemoteOverlayHandle
		select {
		case handle = <-handleCh:
		case <-time.After(5 * time.Second):
			t.Fatal("overlay never opened")
		}
		handle.UpdateLines([]string{marker})
		t.Cleanup(func() { handle.Close(nil) })

		// Setup runs inline (runCtx nil) but after onHandle fires, so poll the
		// editor slot until it settles rather than racing the swap.
		deadline := time.After(3 * time.Second)
		for {
			slot := strings.Join(editorContainer.Render(120), "\n")
			if overlay && !strings.Contains(slot, marker) && editorContainer.ChildCount() == 1 {
				return editorContainer
			}
			if !overlay && strings.Contains(slot, marker) {
				return editorContainer
			}
			select {
			case <-deadline:
				t.Fatalf("editor slot never settled (overlay=%v): %q", overlay, slot)
			case <-time.After(10 * time.Millisecond):
			}
		}
	}

	t.Run("overlay true keeps the editor slot", func(t *testing.T) {
		ec := openOverlay(t, true)
		slot := strings.Join(ec.Render(120), "\n")
		if strings.Contains(slot, marker) {
			t.Fatalf("editor slot was replaced by the overlay; want viewport overlay: %q", slot)
		}
	})

	t.Run("overlay false replaces the editor slot", func(t *testing.T) {
		ec := openOverlay(t, false)
		slot := strings.Join(ec.Render(120), "\n")
		if !strings.Contains(slot, marker) {
			t.Fatalf("editor slot was not replaced inline; want editor-slot render: %q", slot)
		}
	})
}
