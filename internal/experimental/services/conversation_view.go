package services

import "github.com/MichaelKinsy/PiG/durable/harness"

// ConversationView is a durable conversation's active transcript and built-in documents: the raw active entries (the head marker, then the non-head entries from its head) and pi.agent, pi.live, pi.inbox and pi.usage by kind. It is the value of the Transcript service.
type ConversationView = harness.ConversationView

// Document kinds of a ConversationView.
const (
	AgentDocKind = "pi.agent"
	LiveDocKind  = "pi.live"
	InboxDocKind = "pi.inbox"
)
