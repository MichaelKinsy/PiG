package codingagent

import (
	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// subscribeSessionListener subscribes the mode to its Session as Pi's subscribeToAgent does (interactive-mode.ts:3526 `this.session.subscribe`): the Session calls handleSessionEvent after the event's extension handlers and before it persists and publishes the event (agent-session.ts:1208-1230). The owner loop still applies the event to the UI when it arrives on Events.
func (m *InteractiveMode) subscribeSessionListener() {
	m.unsubscribeSessionListener()
	session := m.opts.SessionHandle
	// A new subscription follows renderCurrentSessionState, which drops the streaming component (interactive-mode.ts:2226, :2343).
	m.sessionStreaming.Store(false)
	// upstream: interactive-mode.ts:2097-2099 (bindExtensions hands the Session the abort handler with each binding).
	if binder, ok := session.(interface{ SetExtensionAbortHandler(func()) }); ok {
		binder.SetExtensionAbortHandler(m.extensionAbort)
	}
	m.unsubscribeSession = session.SubscribeFunc(func(event agent.AgentEvent) { m.handleSessionEvent(session, event) })
}

// unsubscribeSessionListener is Pi's `this.unsubscribe?.()` (interactive-mode.ts:2220, :7293).
func (m *InteractiveMode) unsubscribeSessionListener() {
	if m.unsubscribeSession != nil {
		m.unsubscribeSession()
		m.unsubscribeSession = nil
	}
}

// handleSessionEvent is the part of Pi's handleEvent that changes the event's message. Pi's listener is handed the message object the Session persists next and keeps in its transcript, so the errorMessage it gives an aborted message is what the session file holds, what turn_end and agent_end handlers of extensions see (message_end handlers ran before it and saw the provider's), and what a later request hands a provider that reads the transcript.
//
// It runs on the goroutine that executes the Session's event: it writes the message and sessionStreaming only, and the retry count it reads is an atomic of the Session it was subscribed to.
// upstream: packages/coding-agent/src/modes/interactive/interactive-mode.ts:3634 (message_start), :3686-3697 and :3720 (message_end), :3785 (agent_end).
func (m *InteractiveMode) handleSessionEvent(session InteractiveSessionHandle, event agent.AgentEvent) {
	switch event := event.(type) {
	case agent.MessageStartEvent:
		if event.Message.Assistant != nil {
			m.sessionStreaming.Store(true)
		}
	case agent.MessageEndEvent:
		message := event.Message.Assistant
		if message == nil || !m.sessionStreaming.Swap(false) {
			return
		}
		if message.StopReason == ai.StopReasonAborted {
			message.ErrorMessage = abortedMessageForRetries(session.RetryAttempt())
		}
	case agent.AgentEndEvent:
		m.sessionStreaming.Store(false)
	}
}
