package codingagent

// Ports packages/coding-agent/src/modes/interactive/components/session-selector.ts (SessionSelectorHeader.setStatusMessage).

import "time"

// The status timeouts Pi passes to setStatusMessage: a load failure, a rename or delete failure, and a deletion notice.
const (
	sessionSelectorLoadErrorTimeout = 4000 * time.Millisecond // upstream: packages/coding-agent/src/modes/interactive/components/session-selector.ts:setStatusMessage
	sessionSelectorErrorTimeout     = 3000 * time.Millisecond // upstream: packages/coding-agent/src/modes/interactive/components/session-selector.ts:setStatusMessage
	sessionSelectorInfoTimeout      = 2000 * time.Millisecond // upstream: packages/coding-agent/src/modes/interactive/components/session-selector.ts:setStatusMessage
)

type sessionSelectorStatus struct {
	message string
	error   bool
	timer   *time.Timer
}

// setStatusMessage replaces both the message and its timeout. Only the selector owner mutates this state; timers expose a wakeup channel rather than invoking UI code off-owner.
func (s *SessionSelectorComponent) setStatusMessage(message string, isError bool, autoHide time.Duration) {
	if s.statusState.timer != nil {
		s.statusState.timer.Stop()
	}
	s.statusState = sessionSelectorStatus{message: message, error: isError}
	if message != "" && autoHide > 0 {
		s.statusState.timer = time.NewTimer(autoHide)
	}
	s.renderRequested()
}

func (s *SessionSelectorComponent) clearStatusMessage() {
	if s != nil {
		s.setStatusMessage("", false, 0)
	}
}

func (s *SessionSelectorComponent) statusTimeout() <-chan time.Time {
	if s == nil || s.statusState.timer == nil {
		return nil
	}
	return s.statusState.timer.C
}

// expireStatusMessage also supports direct component rendering without an enclosing owner loop.
func (s *SessionSelectorComponent) expireStatusMessage() {
	select {
	case <-s.statusTimeout():
		s.clearStatusMessage()
	default:
	}
}
