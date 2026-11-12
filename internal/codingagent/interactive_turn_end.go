package codingagent

import "slices"

// frameRequested reports whether the renderer has a requested frame that the loop has not run yet. A loop that does not own the frames (a test
// harness whose renderer runs them from its own timer) cannot tell, and says yes.
func (m *InteractiveMode) frameRequested() bool {
	return m.frameTimer == nil || m.frameDue.Load() != 0
}

// turnEndState is what the cleanup at the end of a run found before it changed anything: the rows it may change, and whether a status
// indicator was still shown (a run that was cut short leaves one).
type turnEndState struct {
	rows      []string
	indicator bool
}

// turnEndRows renders the queue rows and the footer.
func (m *InteractiveMode) turnEndRows() []string {
	width := m.tuiInst.Width()
	var rows []string
	if m.pendingMessagesContainer != nil {
		rows = append(rows, m.pendingMessagesContainer.Render(width)...)
	}
	if m.statusLine != nil {
		rows = append(rows, m.statusLine.Render(width)...)
	}
	return rows
}

func (m *InteractiveMode) beginTurnEnd() turnEndState {
	return turnEndState{rows: m.turnEndRows(), indicator: m.activeStatusIndicator != nil}
}

// paintTurnEnd paints what the end of a run still has to paint: a requested frame that has not run, or rows the cleanup changed.
//
// Pi's stream ends with the frame that agent_end requested (interactive-mode.ts:3778 clears the working indicator and calls requestRender once);
// the end of the prompt requests none, and both are one task of its loop, so no frame begins between them. Here the cleanup reaches the loop
// some iterations after agent_end, and the frame of agent_end may have run by then: a paint that changes no row would be a frame after the
// frame that carries the reply (scenario interactive-rendering/68).
func (m *InteractiveMode) paintTurnEnd(before turnEndState) {
	if m.frameRequested() || before.indicator || !slices.Equal(before.rows, m.turnEndRows()) {
		m.tuiInst.Render()
	}
}
