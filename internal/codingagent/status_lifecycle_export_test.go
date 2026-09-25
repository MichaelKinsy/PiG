package codingagent

// NavigateTree drives the production slash-context navigation path on the owner loop.
func (h *TestHarness) NavigateTree(targetID string, summarize bool) (NavigateTreeResult, error) {
	var result NavigateTreeResult
	var err error
	h.Do(func() { result, err = h.m.buildSlashContext(h.ctx).NavigateTreeFull(h.ctx, targetID, summarize, "") })
	return result, err
}

// Status returns the live indicator's kind and label on the owner loop.
func (h *TestHarness) Status() (kind, label string) {
	h.Do(func() {
		if indicator := h.m.activeStatusIndicator; indicator != nil {
			kind, label = indicator.Kind, indicator.Message
		}
	})
	return kind, label
}
