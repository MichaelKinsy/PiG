package codingagent

import (
	"encoding/json"
	"io"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

type overlayInputRecorder struct {
	inputs  []string
	onInput func(string)
}

func (*overlayInputRecorder) Render(int) []string { return []string{"overlay"} }
func (*overlayInputRecorder) Invalidate()         {}
func (r *overlayInputRecorder) HandleInput(data string) {
	r.inputs = append(r.inputs, data)
	if r.onInput != nil {
		r.onInput(data)
	}
}

// Pi tui.ts:1042-1079 restores an eligible overlay before dispatching input, but retains an active blocked replacement until that replacement changes focus.
func TestInteractiveOverlayInputParity(t *testing.T) {
	for _, mode := range []string{"regular", "fullscreen"} {
		t.Run(mode, func(t *testing.T) {
			var renderer tui.Renderer
			if mode == "regular" {
				renderer = tui.NewWithOutput(io.Discard, 80, 24)
			} else {
				renderer = tui.NewTuiAltScreenWithOutput(io.Discard, 80, 24, tui.TuiAltScreenOptions{})
			}
			t.Cleanup(renderer.CancelPendingRender)
			// Keep owner-loop render requests queued; the test drives input in its exact upstream order and cancels pending work at teardown.
			renderer.SetRenderDispatcher(func(func()) {})
			editor := &overlayInputRecorder{inputs: []string{}}
			overlay := &overlayInputRecorder{inputs: []string{}}
			replacement := &overlayInputRecorder{inputs: []string{}}
			m := &InteractiveMode{tuiInst: renderer}
			renderer.SetFocus(editor)
			renderer.OpenOverlay(overlay, tui.OverlayOptions{})
			renderer.SetFocus(editor)
			send := func(data string) {
				t.Helper()
				if err := m.handleKey(t.Context(), data); err != nil {
					t.Fatal(err)
				}
			}
			send("x")
			if !slices.Equal(overlay.inputs, []string{"x"}) || len(editor.inputs) != 0 {
				t.Fatalf("eligible overlay: overlay=%q editor=%q", overlay.inputs, editor.inputs)
			}
			overlay.onInput = func(data string) {
				if data == "b" {
					renderer.SetFocus(replacement)
				}
			}
			replacement.onInput = func(data string) {
				if data == "\r" {
					renderer.SetFocus(editor)
				}
			}
			send("b")
			send("1")
			send("\r")
			send("y")
			if !slices.Equal(overlay.inputs, []string{"x", "b", "y"}) || !slices.Equal(replacement.inputs, []string{"1", "\r"}) || len(editor.inputs) != 0 {
				t.Fatalf("replacement: overlay=%q replacement=%q editor=%q", overlay.inputs, replacement.inputs, editor.inputs)
			}
			if renderer.FocusedComponent() != overlay {
				t.Fatal("overlay did not regain focus")
			}
			observation := struct{ Editor, Overlay, Replacement []string }{editor.inputs, overlay.inputs, replacement.inputs}
			data, err := json.Marshal(observation)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("overlay-input-observation:%s", data)
		})
	}
}

func BenchmarkInteractiveOverlayInput(b *testing.B) {
	for _, tt := range []struct {
		name string
		rows int
	}{{"short", 10}, {"large", 100000}} {
		b.Run(tt.name, func(b *testing.B) {
			renderer := tui.NewWithOutput(io.Discard, 80, 24)
			b.Cleanup(renderer.CancelPendingRender)
			renderer.SetRenderDispatcher(func(func()) {})
			for range tt.rows {
				renderer.Add(tui.NewText("retained transcript"))
			}
			editor := &overlayInputRecorder{}
			overlay := &overlayInputRecorder{}
			renderer.SetFocus(editor)
			renderer.OpenOverlay(overlay, tui.OverlayOptions{})
			renderer.SetFocus(editor)
			m := &InteractiveMode{tuiInst: renderer}
			b.ReportAllocs()
			for b.Loop() {
				if err := m.handleKey(b.Context(), "x"); err != nil {
					b.Fatal(err)
				}
				overlay.inputs = overlay.inputs[:0]
			}
		})
	}
}
