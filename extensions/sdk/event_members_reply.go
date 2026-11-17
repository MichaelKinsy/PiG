package sdk

import (
	"reflect"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// Pi hands every handler of a message_start, message_update, turn_start, agent_start or agent_end emit the one event object (runner.ts emit), so a member a handler
// assigns, reassigns or deletes is what the later handlers of the dispatch see, of this extension and of the others. The host cannot share the map: the reply carries
// the members the handler left different from the ones it received, as {member, value} and {member, deleted: true} entries of _pigEventEdits, and the host writes them
// over the payload of every later request of the dispatch (and of no later dispatch).

// memberEvent tells whether the event is one whose members the reply carries.
func memberEvent(event string) bool {
	switch event {
	case "message_start", "message_update", "turn_start", "agent_start", "agent_end":
		return true
	}
	return false
}

// maxSnapshotMembers is the number of members a snapshot holds without allocating; an event has at most five of its own.
const maxSnapshotMembers = 8

// memberSnapshot is the members of an event as the handler received them: the value of each, by identity (a map or a slice is the same value when it is the same object,
// so an edit inside a message, which the reply's message entries carry, is no edit of the member). It is taken before the handler runs and costs no allocation.
type memberSnapshot struct {
	n     int
	names [maxSnapshotMembers]string
	vals  [maxSnapshotMembers]any
	// over is set when the event had more members than the snapshot holds: the reply then names no member (a payload of the host has no such event).
	over bool
}

func snapshotMembers(data map[string]any) memberSnapshot {
	var s memberSnapshot
	for name, value := range data {
		if s.n == maxSnapshotMembers {
			s.over = true
			break
		}
		s.names[s.n], s.vals[s.n] = name, value
		s.n++
	}
	return s
}

// edits names the members the handler left different from the ones it received: {member, value} for one it added or reassigned, {member, deleted: true} for one it deleted.
// It is nil when the handler changed none.
func (s *memberSnapshot) edits(data map[string]any) []any {
	if s.over {
		return nil
	}
	var edits []any
	seen := 0
	for i := 0; i < s.n; i++ {
		value, ok := data[s.names[i]]
		switch {
		case !ok:
			edits = append(edits, map[string]any{"member": s.names[i], "deleted": true})
		case !sameMember(s.vals[i], value):
			edits = append(edits, map[string]any{"member": s.names[i], "value": value})
		}
		if ok {
			seen++
		}
	}
	if len(data) == seen {
		return edits
	}
	for name, value := range data {
		if !s.has(name) {
			edits = append(edits, map[string]any{"member": name, "value": value})
		}
	}
	return edits
}

func (s *memberSnapshot) has(name string) bool {
	for i := 0; i < s.n; i++ {
		if s.names[i] == name {
			return true
		}
	}
	return false
}

// sameMember tells whether the handler left the member as it received it: a scalar by value, a map or a slice by identity.
func sameMember(was, now any) bool {
	switch a := was.(type) {
	case nil:
		return now == nil
	case bool:
		b, ok := now.(bool)
		return ok && a == b
	case float64:
		b, ok := now.(float64)
		return ok && a == b
	case string:
		b, ok := now.(string)
		return ok && a == b
	case json.Number:
		b, ok := now.(json.Number)
		return ok && a == b
	}
	a, b := reflect.ValueOf(was), reflect.ValueOf(now)
	if !b.IsValid() || a.Type() != b.Type() {
		return false
	}
	switch a.Kind() {
	case reflect.Map:
		return a.Pointer() == b.Pointer()
	case reflect.Slice:
		return a.Pointer() == b.Pointer() && a.Len() == b.Len()
	}
	return a.Type().Comparable() && was == now
}
