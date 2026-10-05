package codingagent

import (
	"context"
	"math/rand/v2"
)

// Ports packages/coding-agent/src/modes/interactive/interactive-mode.ts (handleArminSaysHi and handleDementedDelves).
func (m *InteractiveMode) handleArminSaysHi(ctx context.Context) {
	if m.playPig3d() {
		return
	}
	component := newArminComponent(rand.Float64)
	m.arminComponents = append(m.arminComponents, component)
	m.appendChatBlock(component)
	component.startAnimation(ctx, m.postToMain, m.requestRender)
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
