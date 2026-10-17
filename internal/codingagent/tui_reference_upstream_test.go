package codingagent

import (
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

type countingRenderer struct {
	tui.TUI
	renders, requests int
}

func (r *countingRenderer) Render()               { r.renders++ }
func (r *countingRenderer) RequestRender(...bool) { r.requests++ }

// packages/coding-agent/test/suite/regressions/7731-tui-method-wrapping.test.ts: upstream's createInteractiveTuiReference Proxy is designed out. PiG resolves m.tuiInst at call time, so a method value captured before a renderer replacement is the observable contract.
func TestTUIMethodWrappingUpstream(t *testing.T) {
	// 7731-tui-method-wrapping.test.ts:6 "calls the method captured before a replacement": a captured method wrapped by a replacement still reaches the renderer.
	t.Run("calls the method captured before a replacement", func(t *testing.T) {
		renderer := &countingRenderer{}
		m := &InteractiveMode{tuiInst: renderer}
		originalRender := m.renderNow
		wrapped := func() { originalRender() }
		wrapped()
		if renderer.renders != 1 {
			t.Fatalf("renders=%d, want 1", renderer.renders)
		}
		ui := &TUIUIContext{interactiveMode: m}
		originalUIRender := ui.renderNow
		func() { originalUIRender() }()
		if renderer.renders != 2 {
			t.Fatalf("extension UI renders=%d, want 2", renderer.renders)
		}
	})
	// 7731-tui-method-wrapping.test.ts:17 "routes a captured method to a replacement renderer".
	t.Run("routes a captured method to a replacement renderer", func(t *testing.T) {
		regular, fullscreen := &countingRenderer{}, &countingRenderer{}
		m := &InteractiveMode{tuiInst: regular}
		ui := &TUIUIContext{interactiveMode: m}
		for name, requestRender := range map[string]func(){"mode": m.requestRender, "extension UI": ui.requestRender} {
			m.tuiInst = regular
			regular.requests, fullscreen.requests = 0, 0
			requestRender()
			m.tuiInst = fullscreen
			requestRender()
			if regular.requests != 1 || fullscreen.requests != 1 {
				t.Fatalf("%s: regular=%d fullscreen=%d, want 1 each", name, regular.requests, fullscreen.requests)
			}
		}
	})
}
