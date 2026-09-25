package codingagent

// customMessageRoute is how a custom message from an extension reaches the
// session, mirroring the four branches of upstream sendCustomMessage
// (agent-session.ts:1453-1469).
type customMessageRoute int

const (
	// customMessagePersist appends the entry and renders it without starting a
	// turn. Upstream's final else.
	customMessagePersist customMessageRoute = iota
	// customMessageStartTurn runs a new turn seeded with the message. Upstream's
	// `else if options?.triggerTurn` branch, which calls _runAgentPrompt.
	customMessageStartTurn
	// customMessageQueue hands the message to the running turn, or defers it to
	// the next one. Upstream's nextTurn and isStreaming branches.
	customMessageQueue
)

// routeCustomMessage picks the delivery route for a custom message.
//
// The predicate that matters is whether a turn is in flight, not whether an
// agent object exists. Conflating the two stranded a triggerTurn message
// whenever the agent was idle: it went to a queue that only drains inside a
// running turn, while the persistence branch was skipped because it counted as
// queued, so the message reached neither the UI nor the session.
func routeCustomMessage(hasAgent, streaming, triggerTurn bool, deliverAs string) customMessageRoute {
	if !hasAgent {
		return customMessagePersist
	}
	if deliverAs == "nextTurn" {
		return customMessageQueue
	}
	if streaming {
		return customMessageQueue
	}
	if triggerTurn {
		return customMessageStartTurn
	}
	return customMessagePersist
}
