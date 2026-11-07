package coding

// ObserveReplicatedState registers observe to run after a change to Session state that an extension runtime replicates and that no extension call announced: the active model (set, cycled, restored with a replaced Session, or refreshed from the registry), the thinking level, the session name (set, or replaced with the Session) and the settings.
// Pi's extension context reads each of them live, so `ctx.model`, `pi.getThinkingLevel()`, `pi.getSessionName()` and `pi.getSettings()` return the new value at the next read (runner.ts:906, loader.ts:411-414, agent-session.ts); a runtime replicates the state, so its owner reports the change through this.
// observe runs on the goroutine that made the change, which may be the owner loop or hold a Session or settings lock: it must return at once and must not call back into the Session or its settings. It runs after the change, so a read from another goroutine sees the new state. The returned function removes the observer and can be called repeatedly.
func (s *Session) ObserveReplicatedState(observe func()) func() {
	stopSession := s.replicatedState.Add(observe)
	if settings := s.SettingsManager(); settings != nil {
		stopSettings := settings.ObserveChange(observe)
		return func() {
			stopSession()
			stopSettings()
		}
	}
	return stopSession
}

// notifyReplicatedState reports a change of the Session state an extension runtime replicates (ObserveReplicatedState). The settings report their own.
func (s *Session) notifyReplicatedState() { s.replicatedState.Notify() }

// ObserveReplicatedStateScope registers begin to run before a change to the Session's identity state (the active model, the session id, name and file) becomes visible, and end to run after the observers of ObserveReplicatedState reported it. A reader on another goroutine can see the new state between the two, before any observer ran; the extension host uses the bracket to flush the state before it replies to a call in that stretch. begin and end run on the goroutine that makes the change, hold the same restrictions as an observer, and are called in pairs. The returned function removes them.
func (s *Session) ObserveReplicatedStateScope(begin, end func()) func() {
	stopBegin := s.replicatedBegin.Add(begin)
	stopEnd := s.replicatedEnd.Add(end)
	return func() {
		stopBegin()
		stopEnd()
	}
}

// beginReplicatedChange announces a change of the identity state before it is made, and returns the function that reports it made.
func (s *Session) beginReplicatedChange() (changed func()) {
	s.replicatedBegin.Notify()
	return func() {
		s.replicatedState.Notify()
		s.replicatedEnd.Notify()
	}
}
