package codingagent

import (
	"bytes"
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

type focusedInputProbe struct {
	tui.BaseComponent
	inputs []string
}

func (p *focusedInputProbe) Render(int) []string { return []string{""} }
func (p *focusedInputProbe) HandleInput(data string) {
	p.inputs = append(p.inputs, data)
}

// A mouse-focused generic overlay is the renderer's keyboard focus owner. The
// coding-agent driver must not send its later input to the application editor.
func TestDispatchKeyRoutesInputToRendererFocusedOverlay(t *testing.T) {
	renderer := tui.NewWithOutput(&bytes.Buffer{}, 80, 24)
	t.Cleanup(renderer.CancelPendingRender)
	editor := tui.NewEditor()
	probe := &focusedInputProbe{}
	renderer.SetFocus(editor)
	handle := renderer.OpenOverlay(probe, tui.OverlayOptions{})
	t.Cleanup(handle.Close)
	mode := &InteractiveMode{tuiInst: renderer, editor: editor, isIdle: true}

	if err := mode.dispatchKey(context.Background(), "!"); err != nil {
		t.Fatalf("dispatch focused overlay input: %v", err)
	}
	if len(probe.inputs) != 1 || probe.inputs[0] != "!" {
		t.Fatalf("overlay inputs = %q, want [!]", probe.inputs)
	}
	if got := editor.Text(); got != "" {
		t.Fatalf("focused overlay input reached editor: %q", got)
	}
}
