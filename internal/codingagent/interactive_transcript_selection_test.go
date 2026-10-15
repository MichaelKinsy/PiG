package codingagent

import (
	"bytes"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// Ports interactive-tui.test.ts "drops the fullscreen selection when session entries are re-rendered" (pi 1.1.0, #9311): selection
// coordinates must not survive a transcript rebuild.
func TestRenderSessionEntriesDropsFullscreenSelection(t *testing.T) {
	var out bytes.Buffer
	renderer := tui.NewTuiAltScreenWithOutput(&out, 40, 4, tui.TuiAltScreenOptions{CopyOnSelect: new(false)})
	t.Cleanup(renderer.Stop)
	renderer.Add(tui.NewText("alpha\nbeta\ngamma\ndelta"))
	renderer.Start()
	renderer.HandleViewportInput("\x1b[<0;1;1M")
	renderer.HandleViewportInput("\x1b[<32;4;2M")
	renderer.HandleViewportInput("\x1b[<0;4;2m")
	if !renderer.HasActiveSelection() {
		t.Fatal("drag did not create a selection")
	}

	mode := &InteractiveMode{tuiInst: renderer, altScreen: renderer, chatContainer: tui.NewContainer()}
	mode.renderSessionEntryList(nil, true)

	if renderer.HasActiveSelection() {
		t.Fatal("selection survived a transcript rebuild")
	}
}
