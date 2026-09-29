package codingagent

import (
	"io"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

type keyboardImmediateRenderer struct {
	tui.Renderer
	immediate int
}

func (r *keyboardImmediateRenderer) RequestImmediateRender() { r.immediate++ }

// .upstream/v0.87.1/packages/tui/test/tui-render.test.ts:117
// The component/queue assertion is in TestUpstreamTUIKeyboardRender; this caller guard proves real interactive key dispatch selects that immediate path.
func TestInteractiveKeyboardRequestsImmediateRender(t *testing.T) {
	base := tui.NewWithOutput(io.Discard, 40, 10)
	t.Cleanup(base.CancelPendingRender)
	base.SetRenderDispatcher(func(func()) {})
	renderer := &keyboardImmediateRenderer{Renderer: base}
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
