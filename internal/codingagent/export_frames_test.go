package codingagent

// OwnFrames makes the harness loop run the renderer's throttled frames from its own frame timer, as inputLoop does (installRenderDispatcher). Call it through Do.
func (h *TestHarness) OwnFrames() { h.m.installRenderDispatcher() }

// RunDueFrame runs the frame whose deadline has passed, as the loop does when its frame timer fires. Call it on the loop.
func (h *TestHarness) RunDueFrame() { h.m.runScheduledRender() }
