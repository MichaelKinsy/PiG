package codingagent

import (
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

const pageDownKey = "\x1b[6~"

func newTwoModelSelector() *tui.ModelSelector {
	items := []tui.ModelSelectorItem{{Provider: "p", ID: "one"}, {Provider: "p", ID: "two"}}
	sel := tui.NewModelSelector("Select model", items, items, "p/one")
	sel.SetFilter("p")
	sel.HandleInput("\x1b[B")
	return sel
}

// TestFullscreenViewportKeysPrecedeEditorSlotSelector pins upstream's input
// listener order in fullscreen mode: TuiAltScreen.handleViewportInput
// (tui-alt-screen.ts:740-751) consumes PageDown before the focused
// editor-slot selector, so the model selector never refilters on it. The
// selector loops read input directly from the driver, so the driver must apply
// the viewport first, as the editor path does.
func TestFullscreenViewportKeysPrecedeEditorSlotSelector(t *testing.T) {
	m := newFullscreenProbe(t)
	sel := newTwoModelSelector()
	before := strings.Join(sel.Render(80), "\n")

	if got := m.modalInputChunks(sel, []string{pageDownKey}); len(got) != 0 {
		t.Fatalf("PageDown reached the fullscreen selector: %q", got)
	}
	if got := m.modalInputChunks(sel, []string{"x"}); len(got) != 1 || got[0] != "x" {
		t.Fatalf("printable key = %q, want it delivered to the selector", got)
	}

	input := make(chan []byte, 2)
	input <- []byte(pageDownKey)
	input <- []byte("x")
	if buf, ok := m.readModalInput(input); !ok || string(buf) != "x" {
		t.Fatalf("readModalInput = %q, %v; want the key after PageDown", buf, ok)
	}

	input <- []byte(pageDownKey)
	m.drainModalInput(sel, input, func(chunk string) bool {
		sel.HandleInput(chunk)
		return false
	})
	if after := strings.Join(sel.Render(80), "\n"); after != before {
		t.Fatalf("PageDown changed the fullscreen selector:\n%s\nwant:\n%s", after, before)
	}
}

// TestRegularModeSelectorReceivesPageDown keeps the main-screen contract: no
// viewport listener exists, so PageDown reaches the model selector's search
// input and refilters (model-selector.ts handleInput, filterModels).
func TestRegularModeSelectorReceivesPageDown(t *testing.T) {
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
