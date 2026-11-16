package codingagent

import (
	"io"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// TestRemoteOverlayRetireLeavesTheCallPending pins what a reload does to an extension's ui.custom overlay: Pi's resetExtensionUI removes the overlay "the same way
// done() does, but leave the promises pending" (interactive-mode.ts:2592-2598), so the command that awaits it stays awaiting. The overlay leaves the screen at
// Retire and RunRemoteOverlay does not return; the component's own close then ends the call with its value, as done(result) resolves Pi's promise.
func TestRemoteOverlayRetireLeavesTheCallPending(t *testing.T) {
	// runCtx nil makes RunRemoteOverlay run setup and teardown inline, as in TestRemoteOverlayClosesWithoutFurtherInput.
	m := &InteractiveMode{tuiInst: tui.NewWithOutput(io.Discard, 120, 40)}
	u := &ExtUIContext{m: m}

	type result struct {
		value any
		ok    bool
	}
	handleCh := make(chan extension.RemoteOverlayHandle, 1)
	done := make(chan result, 1)
	go func() {
		value, ok := u.RunRemoteOverlay(
			extension.RemoteOverlayOptions{Title: "ask", Overlay: true},
			overlayInputSpy{onInput: func(string) {}},
			func(h extension.RemoteOverlayHandle) { handleCh <- h },
		)
		done <- result{value, ok}
	}()
	var handle extension.RemoteOverlayHandle
	select {
	case handle = <-handleCh:
	case <-time.After(5 * time.Second):
		t.Fatal("overlay never opened")
	}
	if got := len(m.extensionOverlays); got != 1 {
		t.Fatalf("open extension overlays = %d, want 1", got)
	}
	retirer, ok := handle.(extension.RemoteOverlayRetirer)
	if !ok {
		t.Fatalf("%T is not an extension.RemoteOverlayRetirer", handle)
	}

	retirer.Retire()
	retirer.Retire()
	if got := len(m.extensionOverlays); got != 0 {
		t.Fatalf("open extension overlays after Retire = %d, want 0", got)
	}
	select {
	case got := <-done:
		t.Fatalf("RunRemoteOverlay returned %v after Retire; Pi leaves the call pending", got)
	case <-time.After(300 * time.Millisecond):
	}

	handle.Close("vermilion")
	select {
	case got := <-done:
		if got.value != "vermilion" || !got.ok {
			t.Fatalf("RunRemoteOverlay = %v, %v after the component's own close, want vermilion, true", got.value, got.ok)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the component's own close did not end the retired overlay's call")
	}
}
