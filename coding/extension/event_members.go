package extension

import (
	"context"
	"encoding/json"
)

// EventMember is one top-level member of an event a handler of a dispatch left added, reassigned or deleted: Value is its JSON, or Deleted says it was removed.
// upstream: runner.ts emit hands every handler of a dispatch the one event object, so a member a handler assigns or deletes is what the later handlers of the dispatch read, whether they belong to the same extension or to another, and nothing after the dispatch. An extension process cannot share the object, and the event is a Go value with no field for the members it does not declare: the dispatch keeps the members beside the event and the host writes them over the encoded payload of every later request of the dispatch.
type EventMember struct {
	Name    string
	Value   json.RawMessage
	Deleted bool
}

// AddEventMembers records members as left by a handler of the dispatch ctx belongs to, replacing the record of a member of the same name. It reports false when ctx belongs to no dispatch. Handlers of a dispatch run one after the other.
func AddEventMembers(ctx context.Context, members []EventMember) bool {
	c := FromContext(ctx)
	if c == nil {
		return false
	}
	for _, member := range members {
		replaced := false
		for i := range c.eventMembers {
			if c.eventMembers[i].Name == member.Name {
				c.eventMembers[i], replaced = member, true
				break
			}
		}
		if !replaced {
			c.eventMembers = append(c.eventMembers, member)
		}
	}
	return true
}

// EventMembers returns the members the earlier handlers of the dispatch ctx belongs to left on its event, or nil when none did.
func EventMembers(ctx context.Context) []EventMember {
	if c := FromContext(ctx); c != nil {
		return c.eventMembers
	}
	return nil
}
