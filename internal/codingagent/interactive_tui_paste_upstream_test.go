package codingagent

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

type pasteInputProbe struct {
	tui.BaseComponent
	inputs []string
}

func (p *pasteInputProbe) Render(int) []string     { return nil }
func (p *pasteInputProbe) HandleInput(data string) { p.inputs = append(p.inputs, data) }

type pasteRenderProbe struct {
	tui.TUI
	requests int
}

func (p *pasteRenderProbe) RequestRender(...bool) { p.requests++; p.TUI.RequestRender() }

// .upstream/v0.87.1/packages/coding-agent/test/interactive-tui.test.ts:173
func TestInteractiveTuiRightClickPasteUpstream(t *testing.T) {
	useClipboardTextTestSeams(t, "darwin", nil,
		func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("unused") },
		func() *tui.NativeClipboard {
			return nativeTextHelper(func(context.Context) (*string, error) { return new("clipboard text"), nil })
		},
	)
	mode := newFullscreenProbe(t)
	target := &pasteInputProbe{}
	mode.altScreen.SetFocus(target)
	renderer := &pasteRenderProbe{TUI: mode.tuiInst}
	mode.tuiInst = renderer
	mode.handleRightClickPaste()
	for len(target.inputs) == 0 {
		select {
		case apply := <-mode.uiTaskCh:
			apply()
		case <-t.Context().Done():
			t.Fatal("paste owner ended")
		}
	}
	mode.clipboardReads.Wait()
	if !reflect.DeepEqual(target.inputs, []string{"\x1b[200~clipboard text\x1b[201~"}) || renderer.requests != 1 {
		t.Fatalf("inputs=%q render requests=%d", target.inputs, renderer.requests)
	}
}
