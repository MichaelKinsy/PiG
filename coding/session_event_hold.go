package coding

import "sync"

// agentEventHold stops the Agent at the Session's event listener until it is released.
type agentEventHold struct {
	mu       sync.Mutex
	released chan struct{}
}

// HoldAgentEvents stops the Agent at its next event, before the Session or any later listener sees it, until release
// is called. release may be called more than once.
//
// It models Pi's single event loop during a termination signal's disposal (print-mode.ts:50-63): while the
// session_shutdown handlers run synchronously, a killed tool's exit, a provider response and every other source of
// Agent progress wait for the loop to turn. A mode holds the Agent before it kills tracked children, and releases it
// when a handler yields (see handleryield) or when the disposal ends. Pi's run goes on only from such a turn.
func (s *Session) HoldAgentEvents() (release func()) {
	released := make(chan struct{})
	s.agentEventHold.mu.Lock()
	s.agentEventHold.released = released
	s.agentEventHold.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			s.agentEventHold.mu.Lock()
			if s.agentEventHold.released == released {
				s.agentEventHold.released = nil
			}
			s.agentEventHold.mu.Unlock()
			close(released)
		})
	}
}

// wait blocks while a hold is in place.
func (h *agentEventHold) wait() {
	h.mu.Lock()
	released := h.released
	h.mu.Unlock()
	if released != nil {
		<-released
	}
}

// held reports whether a hold is in place.
func (h *agentEventHold) held() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.released != nil
}
