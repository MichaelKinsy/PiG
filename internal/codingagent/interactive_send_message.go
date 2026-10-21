package codingagent

import (
	"fmt"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// sendExtensionMessage is an extension's pi.sendMessage in interactive mode. It routes the message as upstream
// sendCustomMessage does (agent-session.ts:2323-2359, see routeCustomMessage), except that a message that starts a run goes through this mode's own
// run start.
//
// The entry is written here only on the paths that do not hand the message to the agent. When the agent takes the message, it persists it through
// OnMessagePersist at the point of delivery, so it cannot land between an assistant's tool_use and its tool_result and invalidate session replay.
func (m *InteractiveMode) sendExtensionMessage(msg extension.CustomMessageRef, opts extension.SendMessageOptions) error {
	if m.currentSession() == nil {
		return fmt.Errorf("session not available")
	}
	agentMsg := agent.AgentMessage{Custom: map[string]any{
		"role":       agent.RoleCustom,
		"customType": msg.CustomType,
		"content":    msg.Content,
		"display":    msg.Display,
		"timestamp":  time.Now().UnixMilli(), //portlint:allow clock agent-session.ts:2331 sendCustomMessage stamps `timestamp: Date.now()`; the package's clock seams belong to the logo animations
	}}
	if msg.Details != nil {
		agentMsg.Custom["details"] = msg.Details
	}
	triggers := opts.TriggerTurn != nil && *opts.TriggerTurn
	// idle is the route for a message that no running turn takes: it starts a run when the extension asked for one and is otherwise appended.
	idle := func() error {
		if triggers {
			return m.startCustomTurn(agentMsg, opts.DeliverAs == extension.DeliverAsFollowUp)
		}
		return m.appendCustomMessageNow(msg)
	}
	switch routeCustomMessage(m.agent != nil, m.agent != nil && m.runStreaming(), opts.TriggerTurn, opts.DeliverAs) {
	case customMessageNextTurn:
		m.agent.QueueNextTurn(agentMsg)
		return nil
	case customMessageStartTurn:
		return m.startCustomTurn(agentMsg, opts.DeliverAs == extension.DeliverAsFollowUp)
	case customMessageSteer, customMessageFollowUp:
		followUp := opts.DeliverAs == extension.DeliverAsFollowUp
		if !m.enqueueIfTurnActive(func() {
			if followUp {
				m.agent.FollowUp(agentMsg)
			} else {
				m.agent.Steer(agentMsg)
			}
		}) {
			// The run settled after routing: take the idle branch instead of queueing into a run that will never drain it.
			return idle()
		}
		return m.postToMain(m.runCtx, m.updatePendingMessagesDisplay)
	case customMessageDefer:
		deferred := false
		m.enqueueIfTurnActive(func() {
			if session := m.opts.SessionHandle; session != nil {
				deferred = session.DeferCustomMessage(agentMsg)
			}
		})
		if !deferred {
			return idle()
		}
		return nil
	default:
		return m.appendCustomMessageNow(msg)
	}
}

// appendCustomMessageNow persists a custom message and renders it, starting no run (upstream _appendCustomMessage).
func (m *InteractiveMode) appendCustomMessageNow(msg extension.CustomMessageRef) error {
	entry, err := m.currentSession().customMessageEntry(msg.CustomType, msg.Content, msg.Display, msg.Details, true)
	if err != nil {
		return err
	}
	// The agent renders a message it takes at its message_start (interactive_events.go); only a message that is persisted here without a run is rendered now.
	if msg.Display {
		m.runOnMain(m.runCtx, func() {
			m.appendCustomMessage(entry)
			m.tuiInst.Render()
		})
	}
	return nil
}

// startCustomTurn runs a turn seeded with the message, or queues it as sendCustomMessage does when a run began since the route was chosen, after agent_settled handlers return when one is being dispatched (upstream defers
// the start through _deferredSettledActions).
func (m *InteractiveMode) startCustomTurn(agentMsg agent.AgentMessage, followUp bool) error {
	start := func() {
		// Starting a turn mutates TUI and session state, so it runs on the owner loop. It re-checks there: a turn may have begun since.
		_ = m.postToMain(m.runCtx, func() {
			if m.agent == nil {
				return
			}
			if m.enqueueIfTurnActive(func() {
				if followUp {
					m.agent.FollowUp(agentMsg)
				} else {
					m.agent.Steer(agentMsg)
				}
			}) {
				m.updatePendingMessagesDisplay()
				return
			}
			m.runCustomMessageTurn(m.runCtx, agentMsg)
		})
	}
	if !m.deferSettledAction(start) {
		start()
	}
	return nil
}
