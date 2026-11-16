package sdk

import "reflect"

// Pi hands every tool_result handler the one currentEvent (runner.ts emitToolResult), so a member a handler assigns on it (content, details, isError,
// structuredContent, usage) is what the next handler sees, and an edit made to an object the caller handed over (a block of the content, a member of the
// details) is in the caller's result too (agent-session.ts _afterToolCall reads it back). The host cannot share the map, so the reply names the members the
// handler left different from the ones it sent and, of those, the ones the handler edited in the very object it received.

// toolResultMembers are the members of a tool_result event a handler can change.
var toolResultMembers = [...]string{"content", "details", "isError", "structuredContent", "usage"}

// toolResultReply is the reply to a tool_result handler. received holds the members of the event as the handler received them, result is what the handler
// returned and sent returns a member as the host sent it, decoded again.
//
// The reply is {_pigToolResultResult, _pigToolResultEdits}: the handler's result and, when a member differs from what the host sent, the new values, the
// names of the changed members and the names of those that are still the object the handler received. A Go handler cannot push onto the list it received:
// an append that outgrows it is an assignment of another list, and one that fits is the same list (Go cannot tell the two apart).
func toolResultReply(data, received map[string]any, result any, sent func(member string) any) any {
	members := map[string]any{}
	changed := []string{}
	shared := []string{}
	for _, name := range toolResultMembers {
		now, nowOK := data[name]
		was, wasOK := received[name]
		if nowOK == wasOK && (!nowOK || reflect.DeepEqual(now, sent(name))) {
			continue
		}
		changed = append(changed, name)
		if nowOK {
			members[name] = now
			if wasOK && sameReference(now, was) {
				shared = append(shared, name)
			}
		}
	}
	reply := map[string]any{"_pigToolResultResult": result}
	if len(changed) > 0 {
		reply["_pigToolResultEdits"] = map[string]any{"members": members, "changed": changed, "shared": shared}
	}
	return reply
}

// sameReference reports whether a and b are the same map or the same list.
func sameReference(a, b any) bool {
	left, right := reflect.ValueOf(a), reflect.ValueOf(b)
	if !left.IsValid() || !right.IsValid() || left.Kind() != right.Kind() {
		return false
	}
	switch left.Kind() {
	case reflect.Map, reflect.Slice:
		return !left.IsNil() && left.Pointer() == right.Pointer()
	}
	return false
}
