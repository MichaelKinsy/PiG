package codingagent

// FlushStartupFramesForTest paints, now, the frame a request still holds back: the renderer's HoldRenders keeps back every frame that is requested or scheduled
// meanwhile, and FlushRequestedRender writes the one pending request. After it the renderer has nothing left to write on its own, so a test that reads the terminal
// up to this call has read every write of the startup (the first idle) without a pause.
func (m *InteractiveMode) FlushStartupFramesForTest() {
	if h, ok := m.tuiInst.(interface{ HoldRenders() }); ok {
		h.HoldRenders()
	}
	if f, ok := m.tuiInst.(interface{ FlushRequestedRender() }); ok {
		f.FlushRequestedRender()
	}
}
