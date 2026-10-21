package codingagent

import "github.com/MichaelKinsy/PiG/coding/extension"

// customMessageRoute is how a custom message from an extension reaches the
// session, mirroring the branches of upstream sendCustomMessage
// (agent-session.ts:2323-2359).
type customMessageRoute int

const (
	// customMessagePersist appends the entry and renders it without starting a
	// turn. Upstream's final else.
	customMessagePersist customMessageRoute = iota
	// customMessageStartTurn runs a new turn seeded with the message. Upstream's
	// `else if (options?.triggerTurn)` branch, which calls _startRun.
	customMessageStartTurn
	// customMessageNextTurn holds the message for the next run. Upstream's
	// deliverAs "nextTurn" branch.
	customMessageNextTurn
	// customMessageSteer queues the message on the running turn so it is
	// delivered after the current tool batch. Upstream's streaming branch with
	// any deliverAs but "followUp".
	customMessageSteer
	// customMessageFollowUp queues the message on the running turn so it is
	// delivered once the turn has nothing more to do. Upstream's streaming
	// branch with deliverAs "followUp".
	customMessageFollowUp
	// customMessageDefer holds the message until the running turn's tool
	// results are complete, then appends it without a model call. Upstream's
	// streaming branch with an explicit triggerTurn false.
	customMessageDefer
)

// routeCustomMessage picks the delivery route for a custom message.
//
// The predicates that matter are whether a turn is in flight, not whether an
// agent object exists, and whether triggerTurn is false rather than unset:
// upstream queues a streaming message unless triggerTurn is exactly false, and
// defers it only then.
func routeCustomMessage(hasAgent, streaming bool, triggerTurn *bool, deliverAs extension.DeliverAs) customMessageRoute {
	if !hasAgent {
		return customMessagePersist
	}
	if deliverAs == extension.DeliverAsNextTurn {
		return customMessageNextTurn
	}
	if streaming && (triggerTurn == nil || *triggerTurn) {
		if deliverAs == extension.DeliverAsFollowUp {
			return customMessageFollowUp
		}
		return customMessageSteer
	}
	if triggerTurn != nil && *triggerTurn {
		return customMessageStartTurn
	}
	if streaming {
		return customMessageDefer
	}
	return customMessagePersist
}
