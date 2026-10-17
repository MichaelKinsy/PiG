package codingagent

import (
	"io"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

type keyboardImmediateRenderer struct {
	tui.TUI
	immediate int
}

// DispatchFocusedInput is the renderer's delivery of input to the focused component, which requests the immediate render itself (tui.ts:1115-1117);
// the wrapper counts the driver's deliveries and forwards them.
func (r *keyboardImmediateRenderer) DispatchFocusedInput(data string) {
	r.immediate++
	r.TUI.DispatchFocusedInput(data)
}

// .upstream/v0.87.1/packages/tui/test/tui-render.test.ts:117
// The component/queue assertion is in TestUpstreamTUIKeyboardRender; this caller guard proves real interactive key dispatch hands each key to the renderer's focused-input delivery, which requests the immediate render.
func TestInteractiveKeyboardRequestsImmediateRender(t *testing.T) {
	base := tui.NewWithOutput(io.Discard, 40, 10)
	t.Cleanup(base.CancelPendingRender)
	base.SetRenderDispatcher(func(func()) {})
	renderer := &keyboardImmediateRenderer{TUI: base}
	component := &overlayInputRecorder{}
	renderer.SetFocus(component)
	m := &InteractiveMode{tuiInst: renderer}
	keys := []string{"first", "second", "typed"}
	for _, key := range keys {
		if err := m.handleKey(t.Context(), key); err != nil {
			t.Fatal(err)
		}
	}
	if renderer.immediate != len(keys) {
		t.Fatalf("immediate render requests=%d, want %d", renderer.immediate, len(keys))
	}
	if !slices.Equal(component.inputs, keys) {
		t.Fatalf("input=%q, want %q", component.inputs, keys)
	}
	t.Logf(`keyboard-render-observation:{"name":"keyboard-caller","requests":%d}`, renderer.immediate)
}
