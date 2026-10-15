package codingagent

import (
	"io"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// Pi tui.ts:715-769 keeps a noncapturing overlay mounted while focus and
// visibility switch. The host input route follows the same state, rather than
// letting the remote overlay consume editor keys after unfocus.
func TestRemoteOverlayHandleControlsOwnInputRoute(t *testing.T) {
	mode := &InteractiveMode{tuiInst: tui.NewWithOutput(io.Discard, 100, 40)}
	ui := &ExtUIContext{m: mode}
	value, ok := ui.RunRemoteOverlay(extension.RemoteOverlayOptions{Overlay: true, Layout: &extension.OverlayLayout{NonCapturing: true}}, nil, func(handle extension.RemoteOverlayHandle) {
		overlay := handle.(*customOverlay)
		control := func(action string, hidden, wantHidden, wantFocused bool) {
			t.Helper()
			state, err := overlay.Control(t.Context(), action, hidden, nil)
			if err != nil || state.Hidden != wantHidden || state.Focused != wantFocused {
				t.Fatalf("%s: state=%+v err=%v", action, state, err)
			}
			route, _ := mode.modalRoute()
			if (route != nil) != wantFocused {
				t.Fatalf("%s: input route active=%v, focused=%v", action, route != nil, wantFocused)
			}
		}
		control("", false, false, false)
		control("focus", false, false, true)
		control("unfocus", false, false, false)
		control("setHidden", true, true, false)
		control("focus", false, true, false)
		control("setHidden", false, false, false)
		control("focus", false, false, true)
		handle.Close("closed")
	})
	if !ok || value != "closed" {
		t.Fatalf("overlay result = %v, %v", value, ok)
	}
	if route, _ := mode.modalRoute(); route != nil {
		t.Fatal("overlay input route survived close")
	}
}

// Pi tui.ts:745-775: unfocus({target}) focuses exactly the explicit target, which may be another mounted overlay or null. The host resolves a remote target to the same mounted component and moves the input route with focus.
func TestRemoteOverlayUnfocusExplicitTargetMatchesPi(t *testing.T) {
	mode := &InteractiveMode{tuiInst: tui.NewWithOutput(io.Discard, 100, 40)}
	ui := &ExtUIContext{m: mode}
	handles := make(chan *customOverlay, 2)
	results := make(chan any, 2)
	open := func() {
		value, _ := ui.RunRemoteOverlay(extension.RemoteOverlayOptions{Overlay: true}, nil, func(handle extension.RemoteOverlayHandle) {
			handles <- handle.(*customOverlay)
		})
		results <- value
	}
	go open()
	first := <-handles
	go open()
	second := <-handles
	if mode.tuiInst.GetFocusedComponent() != tui.Component(second) {
		t.Fatal("the newest capturing overlay is not focused")
	}
	state, err := second.Control(t.Context(), "unfocus", false, &extension.RemoteOverlayFocusTarget{Overlay: first})
	if err != nil || state.Focused {
		t.Fatalf("unfocus to first: state=%+v err=%v", state, err)
	}
	if mode.tuiInst.GetFocusedComponent() != tui.Component(first) {
		t.Fatalf("explicit overlay target not focused: %T", mode.tuiInst.GetFocusedComponent())
	}
	if state, err := first.Control(t.Context(), "", false, nil); err != nil || !state.Focused {
		t.Fatalf("target overlay state=%+v err=%v", state, err)
	}
	if state, err := first.Control(t.Context(), "unfocus", false, &extension.RemoteOverlayFocusTarget{}); err != nil || state.Focused {
		t.Fatalf("unfocus to null: state=%+v err=%v", state, err)
	}
	if focused := mode.tuiInst.GetFocusedComponent(); focused != nil {
		t.Fatalf("null target left %T focused", focused)
	}
	first.Close("first")
	second.Close("second")
	for range 2 {
		<-results
	}
}
