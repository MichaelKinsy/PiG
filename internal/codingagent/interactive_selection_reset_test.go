package codingagent

import (
	"io"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// Pi renderSessionEntries drops the fullscreen text selection before it replaces the transcript, because the selection coordinates point into the old one (interactive-mode.ts:4095-4096, #9311).
func TestRenderSessionEntriesDropsFullscreenTextSelection(t *testing.T) {
	m := statusBorderMode(t, false)
	renderer := tui.NewTuiAltScreenWithOutput(io.Discard, 40, 6, tui.TuiAltScreenOptions{})
	t.Cleanup(renderer.Stop)
	renderer.SetCopyOnSelect(false)
	renderer.Add(tui.NewText("selected text"))
	renderer.Start()
	m.tuiInst = renderer
	m.altScreen = renderer

	renderer.HandleViewportInput("\x1b[<0;1;1M")
	renderer.HandleViewportInput("\x1b[<32;6;1M")
	renderer.HandleViewportInput("\x1b[<0;6;1m")
	if !renderer.HasActiveSelection() {
		t.Fatal("selection did not start")
	}
	m.renderSessionEntryList(nil, false)
	if renderer.HasActiveSelection() {
		t.Fatal("rebuilding the transcript kept the old selection")
	}
}
