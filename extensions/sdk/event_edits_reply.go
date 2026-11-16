package sdk

import "reflect"

// Pi hands every message_end handler the one message (runner.ts emitMessageEnd gives each handler an event over currentMessage) and stores that object
// (agent-session.ts appendMessage), so a handler that edits event.message in place changes the message the next handlers, turn_end, agent_end, the entry and
// the next provider request hold, even when it throws afterwards. The host cannot share the map, so the reply carries what the handler left of it.

// eventMessages are the message objects of an event a handler may edit in place, by the position the reply names them at (_pigEventEdits), as the handler received
// them: message_end has one. An element is not a map when the event carries no message object there (a handler that assigns another value to the member of its
// data changes nothing, as assigning event.message changes nothing in Pi: the event is made for each handler).
func eventMessages(event string, data map[string]any) []any {
	if event == "message_end" {
		return []any{data["message"]}
	}
	return nil
}

// sentEventMessages are the messages of the event as the host sent them, decoded again, in the order of eventMessages.
func sentEventMessages(event string, sentMember func(member string) any) []any {
	if event == "message_end" {
		return []any{sentMember("message")}
	}
	return nil
}

// eventEditsReply is the reply to a handler of an event that carries messages. objects are the event's messages as the handler received them, result is what the
// handler returned and sent returns the messages as the host sent them, decoded again.
//
// The reply is {_pigEventEdits, _pigEventResult}: the messages that differ from what the host sent, each at its position, and the handler's result.
func eventEditsReply(objects []any, result any, sent func() []any) any {
	reply := map[string]any{"_pigEventResult": result}
	var was []any
	var edits []any
	for at, object := range objects {
		if _, ok := object.(map[string]any); !ok {
			continue
		}
		if was == nil {
			was = sent()
		}
		if at < len(was) && !reflect.DeepEqual(object, was[at]) {
			edits = append(edits, map[string]any{"at": at, "message": object})
		}
	}
	if edits != nil {
		reply["_pigEventEdits"] = edits
	}
	return reply
}
