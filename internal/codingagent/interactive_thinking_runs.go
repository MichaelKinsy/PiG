package codingagent

// bindThinkingRuns makes every assistant block follow whether an extension of the mode's runner owns thinking runs: it applies the
// current state now, applies each change the extensions make while they run, and stops following the runner it replaces. Call it after
// every assignment of m.newRunner. A reload that replaces the runner replaces the extensions with it, so ownership drops with them.
// pig additive (D214)
func (m *InteractiveMode) bindThinkingRuns() {
	if m.stopThinkingRuns != nil {
		m.stopThinkingRuns()
		m.stopThinkingRuns = nil
	}
	if m.newRunner != nil {
		runner := m.newRunner
		m.stopThinkingRuns = runner.ObserveThinkingRuns(func() {
			// An extension's call arrives on its host's goroutine; the blocks belong to the owner loop.
			m.updateStatusOnOwner(func() {
				if m.newRunner == runner {
					m.applyOwnedThinkingRuns()
				}
			})
		})
	}
	m.applyOwnedThinkingRuns()
}

// ownsThinkingRuns reports whether an extension of the mode's runner owns thinking runs.
func (m *InteractiveMode) ownsThinkingRuns() bool {
	return m.newRunner != nil && m.newRunner.OwnsThinkingRuns()
}

// applyOwnedThinkingRuns sets the current ownership on every assistant block and redraws.
func (m *InteractiveMode) applyOwnedThinkingRuns() {
	owned := m.ownsThinkingRuns()
	for _, block := range m.assistantBlocks {
		block.SetOwnedThinkingRuns(owned)
	}
	if m.tuiInst != nil {
		m.tuiInst.RequestRender()
	}
}
