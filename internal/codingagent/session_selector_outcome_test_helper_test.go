package codingagent

// hookSessionSelectorOutcome attaches the host callbacks of session-selector.ts:757-763 to a selector built without them.
func hookSessionSelectorOutcome(s *SessionSelectorComponent) *sessionSelectorOutcome {
	outcome := &sessionSelectorOutcome{}
	list := s.GetSessionList()
	list.OnSelect, list.OnCancel = outcome.onSelect, outcome.onCancel
	return outcome
}
