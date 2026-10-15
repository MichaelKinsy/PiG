package codingagent

import "github.com/MichaelKinsy/PiG/tui"

// Ports packages/coding-agent/src/modes/interactive/interactive-mode.ts (handleArminSaysHi and handleDementedDelves).
func (m *InteractiveMode) handleArminSaysHi() {
	if m.playPig3d() {
		return
	}
	component := NewArminComponent(currentRendererTUI{TUI: m.tuiInst, m: m})
	m.arminComponents = append(m.arminComponents, component)
	m.appendChatBlock(component)
	m.tuiInst.RequestRender()
}

func (m *InteractiveMode) handleDementedDelves() {
	m.appendChatBlock(newEarendilAnnouncementComponent())
	m.tuiInst.RequestRender()
}

func (m *InteractiveMode) disposeArminComponents() {
	for _, component := range m.arminComponents {
		component.Dispose()
	}
	m.arminComponents = nil
}

// currentRendererTUI is the ui handed to long-lived components: a renderer replacement (fullscreen switch) leaves the component requesting frames from the current renderer, as the single ui of Pi's InteractiveMode does.
type currentRendererTUI struct {
	tui.TUI
	m *InteractiveMode
}

func (c currentRendererTUI) RequestRender(...bool) { c.m.requestRender() }
