package codingagent

import (
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

const pageDownKey = "\x1b[6~"

func newTwoModelSelector() *tui.ModelSelectorComponent {
	items := []tui.ModelSelectorItem{{Provider: "p", ID: "one"}, {Provider: "p", ID: "two"}}
	sel := tui.NewStaticModelSelectorComponent("Select model", items, items, "p/one")
	sel.SetFilter("p")
	sel.HandleInput("\x1b[B")
	return sel
}

// TestFullscreenSelectionKeepsItsPageKeys pins Pi PR #7680 (Pi #7629) for the editor-slot selectors. TuiAltScreen.handleViewportInput
// would consume tui.select.pageDown, which shares its default key with tui.altScreen.pageDown, before the selector. A focused component that
// captures the selection page keys keeps them, so the model selector moves its selection a page and the transcript does not scroll. Every
// other key, and the page key for a component that does not capture it, still follows the listener order of tui-alt-screen.ts:740-751.
// The selector loops read input directly from the driver, so the driver applies the viewport first and skips it for a capturing selector.
func TestFullscreenSelectionKeepsItsPageKeys(t *testing.T) {
	m := newFullscreenProbe(t)
	sel := newTwoModelSelector()
	if got := m.modalInputChunks(sel, []string{pageDownKey}); len(got) != 1 || got[0] != pageDownKey {
		t.Fatalf("PageDown = %q, want it delivered to the capturing selector", got)
	}
	plain := tui.NewText("not a selection")
	if got := m.modalInputChunks(plain, []string{pageDownKey}); len(got) != 0 {
		t.Fatalf("PageDown reached a component that does not capture it: %q", got)
	}
	if got := m.modalInputChunks(sel, []string{"x"}); len(got) != 1 || got[0] != "x" {
		t.Fatalf("printable key = %q, want it delivered to the selector", got)
	}

	input := make(chan []byte, 2)
	input <- []byte(pageDownKey)
	input <- []byte("x")
	if buf, ok := m.readModalInput(input); !ok || string(buf) != "x" {
		t.Fatalf("readModalInput = %q, %v; want the key after PageDown, which yields to the viewport", buf, ok)
	}
	input <- []byte(pageDownKey)
	input <- []byte("x")
	if buf, ok := m.readModalInputFor(sel, input); !ok || string(buf) != pageDownKey {
		t.Fatalf("readModalInputFor = %q, %v; want PageDown for a capturing selector", buf, ok)
	}
}

// TestRegularModeSelectorPageKeyMovesItsSelection: with no viewport listener PageDown reaches the model selector, which moves its selection a
// page (model-selector.ts handleInput, getPageSelectionIndex) and does not refilter the search.
func TestRegularModeSelectorPageKeyMovesItsSelection(t *testing.T) {
	m := newSwitchTuiProbe(t)
	if m.altScreen != nil {
		t.Fatal("probe started in fullscreen")
	}
	sel := newTwoModelSelector()
	if got := m.modalInputChunks(sel, []string{pageDownKey}); len(got) != 1 || got[0] != pageDownKey {
		t.Fatalf("regular-mode PageDown = %q, want it delivered", got)
	}
}

// TestFullscreenViewportKeysPrecedeEditorSlotExtensionUI applies the same
// listener order to a subprocess extension's ui.custom component: in the
// editor slot PageDown scrolls the transcript and never reaches the extension,
// while other keys still do. A focused overlay keeps PageDown, because the
// viewport defers to it (shouldDeferViewportInputToOverlay).
func TestFullscreenViewportKeysPrecedeEditorSlotExtensionUI(t *testing.T) {
	t.Run("editor slot", func(t *testing.T) { checkFullscreenExtensionUIFirstInput(t, false, "k") })
	t.Run("overlay", func(t *testing.T) { checkFullscreenExtensionUIFirstInput(t, true, pageDownKey) })
}

func checkFullscreenExtensionUIFirstInput(t *testing.T, overlay bool, want string) {
	m := newFullscreenProbe(t)
	// runCtx nil runs RunRemoteOverlay's owner work inline; no main loop runs here.
	runCtx := m.runCtx
	m.runCtx = nil
	t.Cleanup(func() { m.runCtx = runCtx })
	u := &ExtUIContext{m: m}
	handleCh := make(chan extension.RemoteOverlayHandle, 1)
	inputs := make(chan string, 4)
	done := make(chan struct{})
	go func() {
		defer close(done)
		u.RunRemoteOverlay(
			extension.RemoteOverlayOptions{Title: "ask", Overlay: overlay},
			overlayInputSpy{onInput: func(data string) { inputs <- data }},
			func(h extension.RemoteOverlayHandle) { handleCh <- h },
		)
	}()
	var handle extension.RemoteOverlayHandle
	select {
	case handle = <-handleCh:
	case <-time.After(5 * time.Second):
		t.Fatal("extension UI never opened")
	}
	deliverModalInput(t, m, []byte(pageDownKey))
	deliverModalInput(t, m, []byte("k"))
	select {
	case got := <-inputs:
		if got != want {
			t.Fatalf("extension UI (overlay=%v) received %q first, want %q", overlay, got, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("extension UI received no input")
	}
	handle.Close(nil)
	<-done
}
