package codingagent

// Ports packages/coding-agent/src/modes/interactive/components/session-selector.ts (SessionSelectorHeader.setStatusMessage).

import "time"

const (
	sessionSelectorLoadErrorTimeout = 4000 * time.Millisecond
	sessionSelectorErrorTimeout     = 3000 * time.Millisecond
	sessionSelectorInfoTimeout      = 2000 * time.Millisecond
)

type sessionSelectorStatus struct {
	message string
	error   bool
	timer   *time.Timer
}

// setStatusMessage replaces both the message and its timeout. Only the selector owner mutates this state; timers expose a wakeup channel rather than invoking UI code off-owner.
func (s *sessionSelector) setStatusMessage(message string, isError bool, autoHide time.Duration) {
	if s.statusState.timer != nil {
		s.statusState.timer.Stop()
	}
	s.statusState = sessionSelectorStatus{message: message, error: isError}
	if message != "" && autoHide > 0 {
		s.statusState.timer = time.NewTimer(autoHide)
	}
}

func (s *sessionSelector) clearStatusMessage() {
	if s != nil {
		s.setStatusMessage("", false, 0)
	}
}

func (s *sessionSelector) statusTimeout() <-chan time.Time {
	if s == nil || s.statusState.timer == nil {
		return nil
	}
	return s.statusState.timer.C
}

// expireStatusMessage also supports direct component rendering without an enclosing owner loop.
func (s *sessionSelector) expireStatusMessage() {
	select {
	case <-s.statusTimeout():
		s.clearStatusMessage()
	default:
	}
}
