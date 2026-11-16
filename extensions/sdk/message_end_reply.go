package sdk

import "reflect"

// Pi hands every message_end handler the one message (runner.ts emitMessageEnd gives each handler an event over currentMessage) and stores that object
// (agent-session.ts appendMessage), so a handler that edits event.message in place changes the message the next handlers, turn_end, agent_end, the entry and
// the next provider request hold, even when it throws afterwards. The host cannot share the map, so the reply carries what the handler left of it.

// messageEndReply is the reply to a message_end handler. object is the event's message member as the handler received it (a handler that assigns another
// value to the member of its data changes nothing, as assigning event.message changes nothing in Pi: the event is made for each handler), result is what the
// handler returned and sent returns the member as the host sent it, decoded again.
//
// The reply is {_pigMessageEndMessage, _pigMessageEndResult}: the message when it differs from what the host sent, and the handler's result.
func messageEndReply(object, result any, sent func() any) any {
	reply := map[string]any{"_pigMessageEndResult": result}
	if _, ok := object.(map[string]any); ok && !reflect.DeepEqual(object, sent()) {
		reply["_pigMessageEndMessage"] = object
	}
	return reply
}
